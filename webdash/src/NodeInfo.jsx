import React from 'react';
import PropTypes from 'prop-types';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableRow from '@mui/material/TableRow';
import { Link } from "react-router-dom";

import { formatTimestamp } from './utils';
import { styles } from './TaskInfo';

const keyCellSx = { ...styles.cell, ...styles.key };
const valueCellSx = { ...styles.cell, ...styles.value };

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
        <TableRow key={key} sx={styles.row}>
          <TableCell sx={keyCellSx}><b>{key}</b></TableCell>
          <TableCell sx={valueCellSx}>{val}</TableCell>
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
          <div><Link to={"/tasks/" + tid}>{tid}</Link><br/></div>
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
        <Table sx={styles.table}>
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
