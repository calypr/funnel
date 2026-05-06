package server

import (
	"encoding/gob"
	"fmt"
	"strings"
	"time"

	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/events"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/plugins/proto"
	"github.com/ohsu-comp-bio/funnel/plugins/shared"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/ohsu-comp-bio/funnel/util/server"
	"github.com/ohsu-comp-bio/funnel/version"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TaskService is a wrapper which handles common TES Task Service operations,
// such as initializing a task when CreateTask is called. The TaskService is backed by
// two parts: a read API which provides the GetTask and ListTasks endpoints, and a write
// API which implements the events.Writer interface. Task creation and cancelation is
// managed by writing events to underlying event writer.
//
// This makes it easier to define task service backends for new databases, and ensures
// that common operations are handled consistently, such as setting IDs, handling 404s,
// GetServiceInfo, etc.
type TaskService struct {
	tes.UnimplementedTaskServiceServer
	Name          string
	Event         events.Writer
	Compute       events.Computer
	Read          tes.ReadOnlyServer
	Log           *logger.Logger
	Config        *config.Config
	Plugin        shared.Authorize
	PluginManager *shared.Manager
}
type contextKey string

const InternalCallKey contextKey = "internalCall"

// LoadPlugins loads plugins for a task.
func (ts *TaskService) DoPluginAction(ctx context.Context, task *tes.Task, taskType proto.Type) (*proto.JobResponse, error) {
	header := map[string]*proto.StringList{}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, fmt.Errorf("Headers not passed from context")
	}
	for k, v := range md {
		// Some special headers start with ':' and cause downstream errors if kept
		if !strings.HasPrefix(k, ":") {
			header[k] = &proto.StringList{Values: v}
		}
	}
	resp, err := ts.Plugin.PluginAction(ts.Config.Plugins.Params, header, ts.Config, task, taskType)
	if err != nil {
		return resp, fmt.Errorf("DoPluginAction: PluginAction failed: %w", err)
	}
	return resp, nil
}

func (ts *TaskService) HandleDoPluginAction(ctx context.Context, task *tes.Task, taskType proto.Type) (*proto.JobResponse, error) {
	gob.Register(&config.TimeoutConfig_Duration{})
	gob.Register(&config.TimeoutConfig_Disabled{})

	pluginResponse, err := ts.DoPluginAction(ctx, task, taskType)
	if err != nil {
		return pluginResponse, fmt.Errorf("Error loading plugins: %v", err)
	}
	if pluginResponse.Code != 200 {
		return pluginResponse, fmt.Errorf(
			"Plugin returned error: code: %d, message: %s, user: %s, task: %v",
			pluginResponse.Code,
			pluginResponse.Message,
			pluginResponse.UserId,
			pluginResponse.Task,
		)
	}
	if pluginResponse.Config == nil {
		return pluginResponse, fmt.Errorf("Plugin returned empty config")
	}
	return pluginResponse, err
}

// CreateTask provides an HTTP/gRPC endpoint for creating a task.
// This is part of the TES implementation.
func (ts *TaskService) CreateTask(ctx context.Context, task *tes.Task) (*tes.CreateTaskResponse, error) {
	if ts.Config.Plugins != nil {
		pluginResponse, err := ts.HandleDoPluginAction(ctx, task, proto.Type_CREATE)
		if err != nil {
			if pluginResponse != nil && pluginResponse.Code != 0 {
				return nil, status.Errorf(server.GRPCCodeFromHTTPStatus(int(pluginResponse.Code)), "%v", err.Error())
			} else {
				return nil, err
			}
		}
		ts.Log.Debug("Plugin", "Response", pluginResponse)
		ctx = context.WithValue(ctx, "pluginResponse", pluginResponse)

		// If using plugin, replace existing task with returned task from plugin
		if pluginResponse.Task != nil {
			task = pluginResponse.Task
		}
	}

	if err := tes.InitTask(task, true); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err.Error())
	}

	err := ts.Compute.CheckBackendParameterSupport(task)
	if err != nil {
		return nil, fmt.Errorf("error from backend: %s", err)
	}

	ctx = context.WithValue(ctx, "Config", ts.Config)
	if err := ts.Event.WriteEvent(ctx, events.NewTaskCreated(task)); err != nil {
		return nil, fmt.Errorf("error creating task: %s", err)
	}

	pluginResponse := ctx.Value("pluginResponse")
	conf := ctx.Value("Config")

	// dispatch to compute backend
	go func() {
		workerCtx := context.Background()

		if pluginResponse != nil {
			workerCtx = context.WithValue(workerCtx, "pluginResponse", pluginResponse)
		}
		if conf != nil {
			workerCtx = context.WithValue(workerCtx, "Config", conf)
		}

		err := ts.Compute.WriteEvent(workerCtx, events.NewTaskCreated(task))
		ts.Log.Debug("submitted task to compute backend", "taskID", task.Id, "error", err)

		if err != nil {
			ts.Log.Debug("writing SystemError event for task", "taskID", task.Id, "error", err)
			err = ts.Event.WriteEvent(workerCtx, events.NewState(task.Id, tes.SystemError))

			if err != nil {
				ts.Log.Error("error writing SystemError event after compute backend submission failure", "taskID", task.Id, "error", err)
			}
		}
	}()

	return &tes.CreateTaskResponse{Id: task.Id}, nil
}

// GetTask calls GetTask on the underlying tes.ReadOnlyServer. If the underlying server
// returns tes.ErrNotFound, TaskService will handle returning the appropriate gRPC error.
func (ts *TaskService) GetTask(ctx context.Context, req *tes.GetTaskRequest) (*tes.Task, error) {
	/*
			TODO: This function gets called internally via the GRPC client in many places.
			To make this work with the plugin, would have to reconfigure the code to bipass the client
			and talk directly to the underlying dbs.

		if ts.Config.Plugins != nil {
			ts.Log.Info("External GetTask request", "taskID", req.Id)
			pluginResponse, err := ts.HandleDoPluginAction(ctx, &tes.Task{Id: req.Id}, proto.Type_GET)
			if err != nil {
				ts.Log.Error("Plugin authorization failed", "taskID", req.Id, "error", err)
				return nil, err
			}
			ts.Log.Debug("Get Task Response: ", pluginResponse)
			ctx = context.WithValue(ctx, "pluginResponse", pluginResponse)
		} else {
			ts.Log.Debug("Internal GetTask request, skipping plugin authorization", "taskID", req.Id)
		}
	*/

	task, err := ts.Read.GetTask(ctx, req)
	if err == tes.ErrNotFound {
		err = status.Errorf(codes.NotFound, "%v: taskID: %s", err.Error(), req.Id)
	}
	return task, err
}

// ListTasks calls ListTasks on the underlying tes.ReadOnlyServer.
func (ts *TaskService) ListTasks(ctx context.Context, req *tes.ListTasksRequest) (*tes.ListTasksResponse, error) {
	return ts.Read.ListTasks(ctx, req)
}

// CancelTask cancels a task
func (ts *TaskService) CancelTask(ctx context.Context, req *tes.CancelTaskRequest) (*tes.CancelTaskResponse, error) {
	result := &tes.CancelTaskResponse{}
	if ts.Config.Plugins != nil {
		pluginResponse, err := ts.HandleDoPluginAction(ctx, nil, proto.Type_CANCEL)
		if err != nil {
			if pluginResponse != nil && pluginResponse.Code != 0 {
				return nil, status.Errorf(server.GRPCCodeFromHTTPStatus(int(pluginResponse.Code)), "%v", err.Error())
			} else {
				return nil, err
			}
		}
		ts.Log.Debug("Plugin", "Response", pluginResponse)
		ctx = context.WithValue(ctx, "pluginResponse", pluginResponse)
	}

	// updated database and other event streams (includes access-checking)
	err := ts.Event.WriteEvent(ctx, events.NewState(req.Id, tes.Canceled))
	if err == tes.ErrNotFound {
		return result, status.Errorf(codes.NotFound, "%v: taskID: %s", err.Error(), req.Id)
	} else if err == tes.ErrNotPermitted {
		return result, status.Errorf(codes.PermissionDenied, "%v: taskID: %s", err.Error(), req.Id)
	} else if err != nil {
		return result, err
	}

	// dispatch to compute backend
	err = ts.Compute.WriteEvent(ctx, events.NewState(req.Id, tes.Canceled))
	if err != nil {
		ts.Log.Error("compute backend failed to cancel task", "taskID", req.Id, "error", err)
	}

	return result, err
}

// GetServiceInfo returns service metadata.
func (ts *TaskService) GetServiceInfo(ctx context.Context, info *tes.GetServiceInfoRequest) (*tes.ServiceInfo, error) {
	resp := &tes.ServiceInfo{
		CreatedAt: "2016-03-21T16:27:49-07:00",
		// TODO: Change this to "mailto:ellrott@ohsu.edu" when support for "mailto:" URL's are
		// added to tes-compliance-suite
		ContactUrl:       "https://ohsu-comp-bio.github.io/funnel/",
		Description:      "Funnel is a toolkit for distributed task execution via a simple, standard API.",
		DocumentationUrl: "https://ohsu-comp-bio.github.io/funnel/",
		Environment:      "development",
		Id:               "org.ga4gh.funnel",
		Name:             ts.Name,
		Organization: map[string]string{
			"name": "OHSU Computational Biology",
			"url":  "https://github.com/ohsu-comp-bio",
		},
		Storage: []string{
			"file:///path/to/local/funnel-storage",
			"s3://ohsu-compbio-funnel/storage",
		},
		TesResourcesBackendParameters: []string{
			"",
		},
		Type: &tes.ServiceType{
			Artifact: "tes",
			Group:    "org.ga4gh",
			Version:  version.Version,
		},
		UpdatedAt: time.Now().Format(time.RFC3339),
		Version:   version.Version,
	}

	/*
		//Task metrics no longer in service info as of TES 1.1
		if c, ok := ts.Read.(metrics.TaskStateCounter); ok {
			resp.TaskStateCounts = make(map[string]int32)
			// Ensure that all states are present in the response, even if zero.
			for key := range tes.State_value {
				resp.TaskStateCounts[key] = 0
			}
			cs, err := c.TaskStateCounts(ctx)
			if err != nil {
				ts.Log.Error("counting task states", "error", err)
			}
			// Override the zero values in the response.
			for key, count := range cs {
				resp.TaskStateCounts[key] = count
			}
		}
	*/

	return resp, nil
}
