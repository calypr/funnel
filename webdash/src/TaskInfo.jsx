import React, { useState } from 'react';
import PropTypes from 'prop-types';
import Divider from '@mui/material/Divider';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableRow from '@mui/material/TableRow';
import Typography from '@mui/material/Typography';
import Checkbox from '@mui/material/Checkbox';
import Tooltip from '@mui/material/Tooltip';

import { formatDate, elapsedTime } from './utils';

const styles = {
  table: {
    width: '100%',
    overflowX: 'auto',
  },
  row: {
    height: '0',
    width: '100%'
  },
  cell: {
    fontSize: '10pt',
    borderBottomStyle: 'none',
    padding:'1px',
  },
  key: {
    width: '20%',
  },
  value: {
    width: '80%',
  },
};

const keyCellSx = { ...styles.cell, ...styles.key };
const valueCellSx = { ...styles.cell, ...styles.value };

function TaskInfoRaw(props) {
  const { task } = props;
  const [showEnv, setShowEnv] = useState(false);

  const renderRow = (key, val, formatFunc) => {
    if ( val ) {
      if (typeof formatFunc === "function") {
        val = formatFunc(val);
      } else if (formatFunc) {
        console.log("renderRow: formatFunc was not a function:", typeof(formatFunc));
      }
    }
    if ( key && (val || val === 0) ) {
      return (
        <TableRow key={key} sx={styles.row}>
          <TableCell sx={keyCellSx}><b>{key}</b></TableCell>
          <TableCell sx={valueCellSx}>{val}</TableCell>
        </TableRow>
      );
    }
    return null;
  };

  const renderResources = (resources) => {
    if ( resources ) {
      const r = resources;
      var s = r.cpuCores + " CPU cores";
      if (r.ramGb) {
        s += ", " + r.ramGb + " GB RAM";
      }
      if (r.diskGb) {
        s += ", " + r.diskGb + " GB disk space";
      }
      if (r.preemptible) {
        s += ", preemptible";
      }
      return (
        <TableRow key='Resources' sx={styles.row}>
          <TableCell sx={keyCellSx}><b>Resources</b></TableCell>
          <TableCell sx={valueCellSx}>{s}</TableCell>
        </TableRow>
      );
    }
    return null;
  };

  const renderTitle = (title) => {
    if (title) {
      return (
        <div>
          <Typography variant="h6">{title}</Typography>
          <Divider />
        </div>
      );
    }
    return null;
  };

  const renderKV = (data, title, defaultPadding='40px 0px 0px 0px') => {
    if ( data ) {
      return (
        <div style={{padding: defaultPadding}}>
          {renderTitle(title)}
          <Table sx={styles.table}>
            <TableBody>
              {Object.keys(data).map(k => (
               <TableRow key={k} sx={styles.row}>
                 <TableCell sx={keyCellSx}>
                   <b>{k}</b>
                 </TableCell>
                 <TableCell sx={{ ...valueCellSx, whiteSpace: 'pre-line', overflowWrap: 'anywhere' }}>
                   {data[k]}
                 </TableCell>
               </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      );
    }
    return null;
  };

  // should we truncate content???
  const renderFileArrays = (files, title) => {
    if ( files && Array.isArray(files)) {
      return (
        <div style={{padding: '40px 0px 0px 0px'}}>
          {renderTitle(title)}
          {files.map(item => (
            renderKV(item, null, '20px 0px 0px 0px')
          ))}
        </div>
      );
    }
    return null;
  };

  const renderMetadata = (logs) => {
    if ( logs && logs.length ) {
      return renderKV(logs[0].metadata, "Metadata");
    }
    return null;
  };

  const renderOutputFileLog = (logs) => {
    if ( logs && logs.length ) {
      return renderFileArrays(logs[0].outputs, "Output File Log");
    }
    return null;
  };

  // should we truncate stdout / stderr ???
  const renderExecutors = (task) => {
    if ( task.executors ) {
      var executors = task.executors;
      const logs = task.logs?.[0]?.logs ?? [];
      const cmdString = function(cmd) {
       return cmd.join(" ");
      };
      const preFormat = function(s) {
        return <pre>{s}</pre>;
      };

      return (
        <div style={{padding: '40px 0px 0px 0px'}}>
          {renderTitle('Executors')}
          {executors.map((exec, index) => (
            <Table sx={styles.table}>
              <TableBody>
                <TableRow key='Cmd' sx={styles.row}>
                  <TableCell sx={keyCellSx}><b>Command</b></TableCell>
                  <TableCell sx={valueCellSx}>{cmdString(exec.command)}</TableCell>
                </TableRow>
                <TableRow key='Image' sx={styles.row}>
                  <TableCell sx={keyCellSx}><b>Image</b></TableCell>
                  <TableCell sx={valueCellSx}>{exec.image}</TableCell>
                </TableRow>
                <TableRow key='Env' sx={styles.row}>
                  <TableCell sx={keyCellSx}>
                    <b>Env</b>
                    <Tooltip title="Show environment variables">
                      <Checkbox
                        checked={showEnv}
                        onChange={() => setShowEnv(!showEnv)}
                        color="primary"
                        slotProps={{ input: { 'aria-label': 'Show environment variables' } }}
                      />
                    </Tooltip>
                  </TableCell>
                  <TableCell sx={valueCellSx}>
                    {showEnv && exec.env && (
                      <Table size="small">
                        <TableBody>
                          {Object.entries(exec.env).map(([k, v]) => (
                            <TableRow key={k} sx={styles.row}>
                              <TableCell sx={keyCellSx}><b>{k}</b></TableCell>
                              <TableCell sx={valueCellSx}>{v}</TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    )}
                  </TableCell>
                </TableRow>
                {renderRow('Workdir', exec.workdir)}
                {logs[index] != undefined &&
                <>
                {renderRow('StartTime', logs[index].start_time, formatDate)}
                {renderRow('EndTime', logs[index].end_time, formatDate)}
                {renderRow('Exit Code', logs[index].exit_code)}
                {renderRow('Stdout', logs[index].stdout, preFormat)}
                {renderRow('Stderr', logs[index].stderr, preFormat)}
                </>
                }
              </TableBody>
            </Table>
          ))}
        </div>
      );
    }
    return null;
  };

  const renderSysLogs = (logs) => {
    const syslogs = logs?.[0]?.system_logs ?? logs?.[0]?.systemLogs;
    if (Array.isArray(syslogs)) {
      const entries = syslogs.map(item => {
        const entry = {};
        for (const [, key, value] of item.matchAll(/(\S+)='((?:\\'|[^'])*)'/g)) {
          if (value !== "") entry[key] = value.replace(/\\'/g, "'");
        }
        return entry;
      });
      return (
        <div style={{padding: '40px 0px 0px 0px'}}>
          {renderTitle('System Logs')}
          {entries.map(e => renderKV(e, null, '20px 0px 0px 0px'))}
        </div>
      );
    }
    return null;
  };

  const renderTask = (task) => {
    if (!task) {
      return null;
    }
    return(
      <div>
        <Table sx={styles.table}>
          <TableBody>
            {renderRow('Name', task.name)}
            {renderRow('ID', task.id)}
            {renderRow('State', task.state)}
            {renderRow('Description', task.description)}
            {renderResources(task.resources)}
            {renderRow('Creation Time', task.creationTime ?? task.creation_time, formatDate)}
            {renderRow('Elapsed Time', task, elapsedTime)}
          </TableBody>
        </Table>

        {/* Tags */}
        {renderKV(task.tags, 'Tags')}

        {/* Metadata */}
        {renderMetadata(task.logs, 'Metadata')}

        {/* Inputs */}
        {renderFileArrays(task.inputs, 'Inputs')}

        {/* Outputs */}
        {renderFileArrays(task.outputs, 'Outputs')}

        {/* Executors */}
        {renderExecutors(task)}

        {/* Output File Logs */}
        {renderOutputFileLog(task.logs, 'Output File Log')}

        {/* System Logs */}
        {renderSysLogs(task.logs, 'System Logs')}

      </div>
    );
  };

  return (
    <div>
      {renderTask(task)}
    </div>
  );
}

TaskInfoRaw.propTypes = {
  task: PropTypes.object.isRequired,
};

const TaskInfo = TaskInfoRaw;
export { TaskInfo, styles };
