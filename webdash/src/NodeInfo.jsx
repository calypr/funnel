import React from 'react';
import PropTypes from 'prop-types';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableRow from '@mui/material/TableRow';
import { Link } from "react-router-dom";

import { formatTimestamp } from './utils';
function NodeInfoRaw(props) {
  const { node } = props;

  const renderRow = (key, val, formatFunc) => {
    if ( val ) {
      if (typeof formatFunc === "function") {
        val = formatFunc(val);
      } else if (formatFunc) {
        console.log("renderRow: formatFunc was not a function:", typeof(formatFunc));
      }
    }
    if ( key && val ) {
      return (
        <TableRow key={key}>
          <TableCell component="th" scope="row">{key}</TableCell>
          <TableCell>{val}</TableCell>
        </TableRow>
      );
    }
    return null;
  };

  const renderTasks = (taskList) => {
    if (!taskList) {
      return null;
    }
    return (
      renderRow('Tasks', taskList.map(tid => ( 
          <div key={tid}><Link className="task-link" to={"/tasks/" + tid}>{tid}</Link></div>
      )))
    );
  };

  const resourceString = (resources) => {
    if ( resources ) {
      const r = resources;
      var s = r.cpus + " CPU cores";
      if (r.ramGb) {
        s += ", " + r.ramGb + " GB RAM";
      }
      if (r.diskGb) {
        s += ", " + r.diskGb + " GB disk space";
      }
      if (r.preemptible) {
        s += ", preemptible";
      }
      return s;
    }
    return null;
  };

  const renderNode = (node) => {
    if (!node) {
      return null;
    }
    return (
      <div>
        <Table className="info-table" aria-label="Node information">
          <TableBody>
            {renderRow('ID', node.id)}
            {renderRow('Hostname', node.hostname)}
            {renderRow('State', node.state)}
            {renderRow('Resources', node.resources, resourceString)}
            {renderRow('Available', node.available, resourceString)}
            {renderRow('Last Ping', node.lastPing, formatTimestamp)}
            {renderRow('Version', node.version)}
            {renderTasks(node.taskIds)}
          </TableBody>
        </Table>
      </div>
    );
  };

  return (
    <div>
      {renderNode(node)}
    </div>
  );
}

NodeInfoRaw.propTypes = {
  node: PropTypes.object.isRequired,
};

const NodeInfo = NodeInfoRaw;
export { NodeInfo };
