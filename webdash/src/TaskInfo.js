import React, { useState } from 'react';
import PropTypes from 'prop-types';
import { withStyles } from '@material-ui/core/styles';
import Divider from '@material-ui/core/Divider';
import Table from '@material-ui/core/Table';
import TableBody from '@material-ui/core/TableBody';
import TableCell from '@material-ui/core/TableCell';
import TableRow from '@material-ui/core/TableRow';
import Typography from '@material-ui/core/Typography';
import classNames from 'classnames';
import Checkbox from '@material-ui/core/Checkbox';
import Tooltip from '@material-ui/core/Tooltip';

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

function TaskInfoRaw(props) {
  const { classes, task } = props;
  const [showEnv, setShowEnv] = useState(false);

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
        <TableRow key={key} className={classes.row}>
          <TableCell className={classNames(classes.cell, classes.key)}><b>{key}</b></TableCell>
          <TableCell className={classNames(classes.cell, classes.value)}>{val}</TableCell>
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
        <TableRow key='Resources' className={classes.row}>
          <TableCell className={classNames(classes.cell, classes.key)}><b>Resources</b></TableCell>
          <TableCell className={classNames(classes.cell, classes.value)}>{s}</TableCell>
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
          <Table className={classes.table}>
            <TableBody>
              {Object.keys(data).map(k => (
               <TableRow key={k} className={classes.row}>
                 <TableCell className={classNames(classes.cell, classes.key)}>
                   <b>{k}</b>
                 </TableCell>
                 <TableCell className={classNames(classes.cell, classes.value)}>
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
      var logs = [{}];
      if ( task.logs && task.logs && task.logs[0].logs ) {
        logs = task.logs[0].logs;
      }
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
            <Table className={classes.table}>
              <TableBody>
                <TableRow key='Cmd' className={classes.row}>
                  <TableCell className={classNames(classes.cell, classes.key)}><b>Command</b></TableCell>
                  <TableCell className={classNames(classes.cell, classes.value)}>{cmdString(exec.command)}</TableCell>
                </TableRow>
                <TableRow key='Image' className={classes.row}>
                  <TableCell className={classNames(classes.cell, classes.key)}><b>Image</b></TableCell>
                  <TableCell className={classNames(classes.cell, classes.value)}>{exec.image}</TableCell>
                </TableRow>
                <TableRow key='Env' className={classes.row}>
                  <TableCell className={classNames(classes.cell, classes.key)}>
                    <b>Env</b>
                    <Tooltip title="Show environment variables">
                      <Checkbox
                        checked={showEnv}
                        onChange={() => setShowEnv(!showEnv)}
                        color="primary"
                        inputProps={{ 'aria-label': 'Show environment variables' }}
                      />
                    </Tooltip>
                  </TableCell>
                  <TableCell className={classNames(classes.cell, classes.value)}>
                    {showEnv && exec.env && (
                      <Table size="small">
                        <TableBody>
                          {Object.entries(exec.env).map(([k, v]) => (
                            <TableRow key={k} className={classes.row}>
                              <TableCell className={classNames(classes.cell, classes.key)}><b>{k}</b></TableCell>
                              <TableCell className={classNames(classes.cell, classes.value)}>{v}</TableCell>
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
    if ( logs && logs.length && logs[0].systemLogs && Array.isArray(logs[0].systemLogs)) {
      const syslogs = logs[0].systemLogs;
      var entryList = syslogs.map(item => {
        var parts = item.split("' ").map(p => p.replace(/'/g, "").split("="));
        return parts;
      });
      var entries = [];
      for (var i in entryList) {        
        var entry = {};
        for (var j in entryList[i]) {
          if (entryList[i][j][1] !== "") {
            entry[entryList[i][j][0]] = entryList[i][j][1];
          }
        }
        entries.push(entry);
      }
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
        <Table className={classes.table}>
          <TableBody>
            {renderRow('Name', task.name)}
            {renderRow('ID', task.id)}
            {renderRow('State', task.state)}
            {renderRow('Description', task.description)}
            {renderResources(task.resources)}
            {renderRow('Creation Time', task.creationTime, formatDate)}
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

const TaskInfo = withStyles(styles)(TaskInfoRaw);
export { TaskInfo, styles };
