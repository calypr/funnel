import React from 'react';
import PropTypes from 'prop-types';
import Typography from '@mui/material/Typography';
import ReactJson from '@microlink/react-json-view';
import { Link, useParams } from "react-router-dom";
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import TableFooter from '@mui/material/TableFooter';
import TablePagination from '@mui/material/TablePagination';
import IconButton from '@mui/material/IconButton';
import KeyboardArrowLeft from '@mui/icons-material/KeyboardArrowLeft';
import KeyboardArrowRight from '@mui/icons-material/KeyboardArrowRight';
import Paper from '@mui/material/Paper';

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
                onPageChange={function(event, number) { return; }}
                page={0}
                count={0}
                labelDisplayedRows={({ from, to, count }) => ""}
                rowsPerPage={pageSize}
                onRowsPerPageChange={(event) => setPageSize(Number(event.target.value))}
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

async function getAllNodes(signal) {
  const nodes = [];
  const seenTokens = new Set();
  let pageToken = "";

  while (true) {
    const url = new URL("/v1/nodes", window.location.origin);
    url.searchParams.set("page_size", "1000");
    if (pageToken !== "") {
      url.searchParams.set("page_token", pageToken);
    }

    const result = await get(url, false, signal);
    if (!result || !Array.isArray(result.nodes)) {
      throw new Error("ListNodes returned an invalid response");
    }
    nodes.push(...result.nodes);

    const nextPageToken = result.next_page_token ?? result.nextPageToken ?? "";
    if (typeof nextPageToken !== "string") {
      throw new Error("ListNodes returned an invalid page token");
    }
    if (nextPageToken === "") {
      return nodes;
    }
    if (seenTokens.has(nextPageToken)) {
      throw new Error("ListNodes returned a repeated page token");
    }
    seenTokens.add(nextPageToken);
    pageToken = nextPageToken;
  }
}

function NodeList() {
  const [nodes, setNodes] = React.useState(null);
  const [loadError, setLoadError] = React.useState(false);
  //const [nodes, setNodes] = React.useState(example_node_list);

  const nTasks = (node) => {
    if (node.task_ids !== undefined) {
      return node.task_ids.length;
    }
    return 0;
  };

  React.useEffect(() => {
    let active = true;
    let inFlight = false;
    let controller;
    const refresh = () => {
      if (inFlight) {
        return;
      }
      inFlight = true;
      const requestController = new AbortController();
      controller = requestController;
      getAllNodes(requestController.signal)
        .then((allNodes) => {
          if (!active) return;
          setNodes(allNodes);
          setLoadError(false);
        })
        .catch((error) => {
          if (!active || error.name === "AbortError") return;
          console.log("listNodes err:", error.toString());
          setLoadError(true);
        })
        .finally(() => {
          if (controller === requestController) {
            controller = undefined;
          }
          inFlight = false;
        });
    };
    refresh();
    const timer = setInterval(refresh, 10000);
    return () => {
      active = false;
      clearInterval(timer);
      controller?.abort();
    };
  }, []);

  const activeNodes = (nodes ?? []).filter(node => node.state === 'ALIVE');
  const fields = [
    { key: 'cpus', label: 'CPU', unit: 'cores' },
    { key: 'ramGb', label: 'RAM', unit: 'GB' },
    { key: 'diskGb', label: 'Disk', unit: 'GB' },
  ];
  const resourceTotal = (kind, field, unit) => {
    if (activeNodes.some(node => !node[kind] || (node[kind][field] != null &&
      (typeof node[kind][field] !== 'number' || !Number.isFinite(node[kind][field]) || node[kind][field] < 0)))) {
      return 'Unavailable';
    }
    const total = activeNodes.reduce((sum, node) => sum + (node[kind][field] ?? 0), 0);
    return `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 }).format(total)} ${unit}`;
  };

  return (
    <div>
      <Typography variant="h4" gutterBottom component="h2">
        Nodes
      </Typography>
      {loadError && <Typography>Could not load nodes.</Typography>}
      {nodes !== null && (activeNodes.length === 0 ? (
        <Typography>No active nodes</Typography>
      ) : (
        <Paper sx={{ mb: 2, p: 2 }}>
          <Typography variant="h6" component="h3">Resources on active nodes</Typography>
          <Table aria-label="Resources on active nodes" size="small">
            <TableHead><TableRow><TableCell>Resource</TableCell><TableCell>Available</TableCell><TableCell>Total</TableCell></TableRow></TableHead>
            <TableBody>
              {fields.map(({ key, label, unit }) => (
                <TableRow key={key}>
                  <TableCell>{label}</TableCell>
                  <TableCell>{resourceTotal('available', key, unit)}</TableCell>
                  <TableCell>{resourceTotal('resources', key, unit)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Paper>
      ))}
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
            {(nodes ?? []).map(n => (
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
    const url = new URL("/v1/tasks/" + task_id, window.location.origin);
    const controller = new AbortController();
    let active = true;
    let timer;
    const refresh = async () => {
      try {
        const updated = await get(url, false, controller.signal);
        if (!active) return;
        setTask(updated);
        if (isDone(updated)) return;
      } catch (error) {
        if (!active) return;
        console.log("getTask error:", error);
      }
      timer = setTimeout(refresh, 10000);
    };
    refresh();
    return () => {
      active = false;
      clearTimeout(timer);
      controller.abort();
    };
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
      }).catch((error) => console.log("getNode error:", error));
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
      }).catch((error) => console.log("getServiceInfo error:", error));
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
