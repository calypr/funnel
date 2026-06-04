import React from 'react';
import PropTypes from 'prop-types';
import Typography from '@material-ui/core/Typography';
import ReactJson from 'react-json-view';
import { Link, useParams } from "react-router-dom";
import Table from '@material-ui/core/Table';
import TableBody from '@material-ui/core/TableBody';
import TableCell from '@material-ui/core/TableCell';
import TableHead from '@material-ui/core/TableHead';
import TableRow from '@material-ui/core/TableRow';
import TableFooter from '@material-ui/core/TableFooter';
import TablePagination from '@material-ui/core/TablePagination';
import IconButton from '@material-ui/core/IconButton';
import KeyboardArrowLeft from '@material-ui/icons/KeyboardArrowLeft';
import KeyboardArrowRight from '@material-ui/icons/KeyboardArrowRight';
import Paper from '@material-ui/core/Paper';

import { NodeInfo } from './NodeInfo';
import { SystemInfo } from './SystemInfo';
import { TaskInfo } from './TaskInfo';
import { CancelButton } from './CancelButton';
import { get, isDone } from './utils';
import { SimpleTabs } from './Tabs';
import { formatDate, elapsedTime } from './utils';
//import { example_task, example_node, example_service_info, example_task_list, example_node_list } from './ExampleData.js';

function TaskList({pageToken, setPageToken,
                   nextPageToken, setNextPageToken,
                   prevPageToken, setPrevPageToken,
                   pageSize, setPageSize,
                   stateFilter, tagsFilter}) {
  const [tasks, setTasks] = React.useState([]);
  //const [tasks, setTasks] = React.useState(example_task_list);

  const nextPage = () => {
    const next = nextPageToken;
    const current = pageToken;
    var prev = [...prevPageToken, current];
    setPrevPageToken(prev);
    setPageToken(next);
  };

  const prevPage = () => {
    var prev = [...prevPageToken];
    const page = prev.pop();
    setPrevPageToken(prev);
    setPageToken(page);
  };

  React.useEffect(() => {
    var url = new URL("/v1/tasks" + window.location.search, window.location.origin);
    var params = url.searchParams;
    params.set("view", "BASIC");
    params.set("pageSize", pageSize);
    if (stateFilter !== "") {
      params.set("state", stateFilter);
    }
    for (var i = 0; i < tagsFilter.length; i++) {
      var tag = tagsFilter[i];
      if (tag.key !== "") {
        params.set("tag_key", tag.key);
      }
      if (tag.value !== "") {
        params.set("tag_value", tag.value);
      }
    }
    if (pageToken !== "") {
      params.set("pageToken", pageToken);
    }
    get(url)
      .then(
        (result) => {
          var tasks = [];
          if (result.tasks !== undefined) {
            tasks = result.tasks;
          }
          var nextPageToken = "";
          if (result.nextPageToken !== undefined) {
            nextPageToken = result.nextPageToken;
          }
          setTasks(tasks);
          setNextPageToken(nextPageToken);
        },
        (error) => {
          console.log("listTasks", url.toString(), "error:", error);
        },
      );
  }, [stateFilter, tagsFilter, pageSize, pageToken]);

  return (
    <div>
      <Typography variant="h4" gutterBottom component="h2">
        Tasks
      </Typography>
      <Paper style={{minWidth: "250px", width: "100%", overflowX: "auto"}}>
        <Table style={{width: "100%"}}>
          <TableHead>
            <TableRow>
              <TableCell>ID</TableCell>
              <TableCell>State</TableCell>
              <TableCell>Name</TableCell>
              <TableCell>Created At</TableCell>
              <TableCell>Elapsed Time</TableCell>
              <TableCell></TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {tasks.map(t => (
              <TableRow hover key={t.id} style={{height: "63px"}}>
                <TableCell style={{width: '15%'}}><Link to={"/tasks/" + t.id}>{t.id}</Link></TableCell>
                <TableCell style={{width: '15%'}}>{t.state}</TableCell>
                <TableCell style={{width: '30%'}}>{t.name}</TableCell>
                <TableCell style={{width: '15%'}}>{formatDate(t.creationTime)}</TableCell>
                <TableCell style={{width: '15%'}}>{elapsedTime(t)}</TableCell>
                <TableCell style={{width: '10%'}}><CancelButton task={t} /></TableCell>
              </TableRow>
            ))}
          </TableBody>
          <TableFooter>
            <TableRow>
              <TablePagination
                rowsPerPageOptions={[25, 50, 100, 250, 500]}
                onChangePage={function(event, number) { return; }}
                page={0}
                count={0}
                labelDisplayedRows={({ from, to, count }) => ""}
                rowsPerPage={pageSize}
                onChangeRowsPerPage={(event) => setPageSize(event.target.value)}
                ActionsComponent={
                  (actions) => {
                    return (
                      <div style={{flexShrink: 0}}>
                        <IconButton
                          onClick={(event) => prevPage()}
                          disabled={prevPageToken.length === 0}
                          aria-label="Previous Page"
                        >
                          <KeyboardArrowLeft />
                        </IconButton>
                        <IconButton
                          onClick={(event) => nextPage()}
                          disabled={nextPageToken === ""}
                          aria-label="Next Page"
                        >
                          <KeyboardArrowRight />
                        </IconButton>
                      </div>
                    );
                  }
                }
              />
            </TableRow>
          </TableFooter>
        </Table>
      </Paper>
    </div>
  );
};

TaskList.propTypes = {
  pageToken: PropTypes.string.isRequired,
  setPageToken: PropTypes.func.isRequired,
  nextPageToken: PropTypes.string.isRequired,
  setNextPageToken: PropTypes.func.isRequired,
  prevPageToken: PropTypes.array.isRequired,
  setPrevPageToken: PropTypes.func.isRequired,
  pageSize: PropTypes.number.isRequired,
  setPageSize: PropTypes.func.isRequired,
  stateFilter: PropTypes.string.isRequired,
  tagsFilter: PropTypes.array.isRequired,
};

function NodeList() {
  const [nodes, setNodes] = React.useState([]);
  //const [nodes, setNodes] = React.useState(example_node_list);

  const nTasks = (node) => {
    if (node.task_ids !== undefined) {
      return node.task_ids.length;
    }
    return 0;
  };

  React.useEffect(() => {
    get(new URL("/v1/nodes", window.location.origin))
      .then(
        (result) => {
          if (result.nodes !== undefined) {
            setNodes(result.nodes);
          }
        },
        (error) => {
          console.log("listNodes err:", error.toString());
        },
      );
  });

  return (
    <div>
      <Typography variant="h4" gutterBottom component="h2">
        Nodes
      </Typography>
      <Paper style={{minWidth: "250px", width: "100%", overflowX: "auto"}}>
        <Table style={{width: "100%"}}>
          <TableHead>
            <TableRow>
              <TableCell>ID</TableCell>
              <TableCell>Hostname</TableCell>
              <TableCell>State</TableCell>
              <TableCell>Tasks</TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {nodes.map(n => (
              <TableRow hover key={n.id}>
                <TableCell><Link to={"/nodes/" + n.id}>{n.id}</Link></TableCell>
                <TableCell>{n.hostname}</TableCell>
                <TableCell>{n.state}</TableCell>
                <TableCell>{nTasks(n)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Paper>
    </div>
  );
};

function Task() {
  let { task_id } = useParams();
  const [task, setTask] = React.useState({});
  //const [task, setTask] = React.useState(example_task);

  React.useEffect(() => {
    var url = new URL("/v1/tasks/" + task_id, window.location.origin);
    get(url).then(
      (task) => {
        setTask(task);
      });
  }, [task_id]);

  const json = (
    <ReactJson
      src={task}
      theme={"rjv-default"}
      name={false}
      displayObjectSize={false}
      displayDataTypes={false}
      enableClipboard={true}
      collapsed={false}
      shouldCollapse={(field) => {
        // Collapse executors.*.env fields
        if (field.name === "env" && field.namespace.includes("executors")) {
          return true;
        }
        return false;
      }}
    />
  );

  const header = (
    <div>
      <Typography variant="h4" gutterBottom>
        Task: {task_id}
      </Typography>
      <CancelButton task={task} />
      {!isDone(task) && (<br/>)}
    </div>
  );

  return (
    SimpleTabs(header, <TaskInfo task={task} />, json)
  );
};

function Node() {
  let { node_id } = useParams();
  const [node, setNode] = React.useState({});
  //const [node, setNode] = React.useState(example_node);

  React.useEffect(() => {
    var url = new URL("/v1/nodes/" + node_id, window.location.origin);
    get(url).then(
      (node) => {
        setNode(node);
      });
  }, [node_id]);

  const json = (
    <ReactJson
      src={node}
      theme={"rjv-default"}
      name={false}
      displayObjectSize={false}
      displayDataTypes={false}
      enableClipboard={true}
      collapsed={false}
    />
  );

  const header = (
    <div>
      <Typography variant="h4" gutterBottom component="h2">
        Node: {node_id}
      </Typography>
    </div>
  );

  return (
    SimpleTabs(header, <NodeInfo node={node} />, json)
  );
};

function ServiceInfo() {
  const [info, setInfo] = React.useState({});
  //const [info, setInfo] = React.useState(example_service_info);

  React.useEffect(() => {
    var url = new URL("/v1/service-info", window.location.origin);
    get(url).then(
      (info) => {
        setInfo(info);
      });
  }, []);

  const json = (
    <ReactJson
      src={info}
      theme={"rjv-default"}
      name={false}
      displayObjectSize={false}
      displayDataTypes={false}
      enableClipboard={true}
      collapsed={false}
    />
  );

  const header = (
    <div>
      <Typography variant="h4" gutterBottom component="h2">
        System Info
      </Typography>
    </div>
  );

  return (
    SimpleTabs(header, <SystemInfo info={info} />, json)
  );
};

function NoMatch() {
  return (
    <Typography variant="h4" gutterBottom component="h2" style={{color: "red"}}>
      404 Not Found
    </Typography>
  );
};

export {TaskList, Task, NodeList, Node, ServiceInfo, NoMatch};
