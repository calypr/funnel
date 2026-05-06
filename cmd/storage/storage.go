package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	cmdutil "github.com/ohsu-comp-bio/funnel/cmd/util"
	"github.com/ohsu-comp-bio/funnel/config"
	"github.com/ohsu-comp-bio/funnel/logger"
	"github.com/ohsu-comp-bio/funnel/storage"
	"github.com/ohsu-comp-bio/funnel/tes"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
)

var log = logger.NewLogger("storage", logger.DefaultConfig())

func newStorage(conf *config.Config) (storage.Storage, error) {
	store, err := storage.NewMux(conf)
	if err != nil {
		return nil, err
	}
	store.AttachLogger(log)
	return store, nil
}

// NewCommand returns the "storage" subcommands.
func NewCommand() *cobra.Command {

	configFile := ""
	conf := &config.Config{}
	flagConf := &config.Config{}

	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Access storage via Funnel's client libraries.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			var err error
			conf, err = cmdutil.MergeConfigFileWithFlags(configFile, flagConf)
			if err != nil {
				return fmt.Errorf("processing config: %v", err)
			}
			return nil
		},
	}
	cmd.SetGlobalNormalizationFunc(cmdutil.NormalizeFlags)
	f := cmd.PersistentFlags()
	f.StringVarP(&configFile, "config", "c", configFile, "Config File")

	statCmd := &cobra.Command{
		Use:   "stat [url]",
		Short: "Returns information about the object at the given URL.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cmd.Usage()
			}

			store, err := newStorage(conf)
			if err != nil {
				return fmt.Errorf("creating storage clients: %s", err)
			}

			obj, err := store.Stat(context.Background(), args[0])
			if err != nil {
				return err
			}

			b, err := json.Marshal(obj)
			if err != nil {
				return fmt.Errorf("marshaling output: %s", err)
			}
			fmt.Println(string(b))
			return nil
		},
	}

	statTaskCmd := &cobra.Command{
		Use:   "stat-task [task file]",
		Short: "Returns information about inputs/outputs of the task.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cmd.Usage()
			}

			store, err := newStorage(conf)
			if err != nil {
				return fmt.Errorf("creating storage clients: %s", err)
			}

			f, err := os.Open(args[0])
			if err != nil {
				return fmt.Errorf("opening task file: %s", err)
			}
			defer f.Close()

			dec := json.NewDecoder(f)
			for {
				task := &tes.Task{}
				var jsonData json.RawMessage
				err := dec.Decode(&jsonData)
				if err == io.EOF {
					break
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %s\n", err)
					continue
				}

				err = protojson.Unmarshal(jsonData, task)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %s\n", err)
					continue
				}

				for _, in := range task.Inputs {
					obj, err := store.Stat(context.Background(), in.Url)
					if err != nil {
						fmt.Fprintf(os.Stderr, "error: %s\n", err)
						continue
					}

					b, _ := json.Marshal(obj)
					fmt.Println(string(b))
				}

				for _, out := range task.Outputs {
					obj, err := store.Stat(context.Background(), out.Url)
					if err != nil {
						fmt.Fprintf(os.Stderr, "error: %s\n", err)
						continue
					}

					b, _ := json.Marshal(obj)
					fmt.Println(string(b))
				}
			}

			return nil
		},
	}

	listCmd := &cobra.Command{
		Use:   "list [url]",
		Short: "List objects at the given URL.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return cmd.Usage()
			}

			store, err := newStorage(conf)
			if err != nil {
				return fmt.Errorf("creating storage clients: %s", err)
			}

			objects, err := store.List(context.Background(), args[0])
			if err != nil {
				return err
			}

			b, err := json.Marshal(objects)
			if err != nil {
				return fmt.Errorf("marshaling output: %s", err)
			}
			fmt.Println(string(b))
			return nil
		},
	}

	getCmd := &cobra.Command{
		Use:   "get [url] [path]",
		Short: "Get the object at the given URL.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return cmd.Usage()
			}

			store, err := storage.NewMux(conf)
			if err != nil {
				return fmt.Errorf("creating storage clients: %s", err)
			}

			obj, err := store.Get(context.Background(), args[0], args[1])
			if err != nil {
				return err
			}

			b, err := json.Marshal(obj)
			if err != nil {
				return fmt.Errorf("marshaling output: %s", err)
			}
			fmt.Println(string(b))
			return nil
		},
	}

	putCmd := &cobra.Command{
		Use:   "put [path] [url]",
		Short: "Put the local file to the given URL.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return cmd.Usage()
			}

			store, err := storage.NewMux(conf)
			if err != nil {
				return fmt.Errorf("creating storage clients: %s", err)
			}

			obj, err := store.Put(context.Background(), args[1], args[0])
			if err != nil {
				return err
			}

			b, err := json.Marshal(obj)
			if err != nil {
				return fmt.Errorf("marshaling output: %s", err)
			}
			fmt.Println(string(b))
			return nil
		},
	}

	cmd.AddCommand(getCmd)
	cmd.AddCommand(putCmd)
	cmd.AddCommand(listCmd)
	cmd.AddCommand(statCmd)
	cmd.AddCommand(statTaskCmd)
	return cmd
}
