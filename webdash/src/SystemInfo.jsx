import React from 'react';
import PropTypes from 'prop-types';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableRow from '@mui/material/TableRow';

import { styles } from './TaskInfo';

const keyCellSx = { ...styles.cell, ...styles.key };
const valueCellSx = { ...styles.cell, ...styles.value };

function SystemInfoRaw(props) {
  const { info } = props;

  const renderStateCounts = () => {
    if (info.taskStateCounts === undefined) {
      return (
        <TableRow sx={styles.row} key="counts">
          <TableCell sx={keyCellSx}><b>Task State Counts</b></TableCell>
          <TableCell sx={valueCellSx}>Not Available</TableCell>
        </TableRow>
      );
    } else {
      return (
        <TableRow sx={styles.row} key="counts">
          <TableCell sx={keyCellSx}><b>Task State Counts</b></TableCell>
          <TableCell sx={valueCellSx}>
            <Table>
              <TableBody>
                {Object.keys(info.taskStateCounts).map(k => (
                  <TableRow sx={styles.row} key={k}>
                    <TableCell sx={keyCellSx}>{k}</TableCell>
                    <TableCell sx={valueCellSx}>{info.taskStateCounts[k]}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableCell>
        </TableRow>
      );
    }
  };

  return (
    <Table sx={styles.table}>
      <TableBody>
        <TableRow sx={styles.row} key="contactUrl">
          <TableCell sx={keyCellSx}><b>contactUrl</b></TableCell>
          <TableCell sx={valueCellSx}><pre>{info.contactUrl}</pre></TableCell>
        </TableRow>
        <TableRow sx={styles.row} key="createdAt">
          <TableCell sx={keyCellSx}><b>createdAt</b></TableCell>
          <TableCell sx={valueCellSx}><pre>{info.createdAt}</pre></TableCell>
        </TableRow>
        <TableRow sx={styles.row} key="description">
          <TableCell sx={keyCellSx}><b>description</b></TableCell>
          <TableCell sx={valueCellSx}><pre>{info.description}</pre></TableCell>
        </TableRow>
        <TableRow sx={styles.row} key="documentationUrl">
          <TableCell sx={keyCellSx}><b>documentationUrl</b></TableCell>
          <TableCell sx={valueCellSx}><pre>{info.documentationUrl}</pre></TableCell>
        </TableRow>
        <TableRow sx={styles.row} key="id">
          <TableCell sx={keyCellSx}><b>id</b></TableCell>
          <TableCell sx={valueCellSx}><pre>{info.id}</pre></TableCell>
        </TableRow>
        <TableRow sx={styles.row} key="organization">
          <TableCell sx={keyCellSx}><b>organization</b></TableCell>
          <TableBody>
            <TableRow sx={styles.row}>
              <TableCell sx={valueCellSx}><pre>{info.organization?.name}</pre></TableCell>
            </TableRow>
            <TableRow>
              <TableCell sx={valueCellSx}><pre>{info.organization?.url}</pre></TableCell>
            </TableRow>
          </TableBody>
        </TableRow>
        <TableRow sx={styles.row} key="storage">
          <TableCell sx={keyCellSx}><b>storage</b></TableCell>
          <TableCell sx={valueCellSx}><pre>{info.storage}</pre></TableCell>
        </TableRow>
        <TableRow sx={styles.row} key="tesResources_backend_parameter">
          <TableCell sx={keyCellSx}><b>tesResources_backend_parameter</b></TableCell>
          <TableCell sx={valueCellSx}><pre>{info.tesResources_backend_parameter}</pre></TableCell>
        </TableRow>
        <TableRow sx={styles.row} key="type">
          <TableCell sx={keyCellSx}><b>type</b></TableCell>
          <TableBody>
            <TableRow sx={styles.row}>
              <TableCell sx={valueCellSx}><pre>{info.type?.artifact}</pre></TableCell>
            </TableRow>
            <TableRow>
              <TableCell sx={valueCellSx}><pre>{info.type?.group}</pre></TableCell>
            </TableRow>
            <TableRow>
              <TableCell sx={valueCellSx}><pre>{info.type?.version}</pre></TableCell>
            </TableRow>
          </TableBody>
        </TableRow>
        <TableRow sx={styles.row} key="updatedAt">
          <TableCell sx={keyCellSx}><b>updatedAt</b></TableCell>
          <TableCell sx={valueCellSx}><pre>{info.updatedAt}</pre></TableCell>
        </TableRow>
        <TableRow sx={styles.row} key="version">
          <TableCell sx={keyCellSx}><b>version</b></TableCell>
          <TableCell sx={valueCellSx}><pre>{info.version}</pre></TableCell>
        </TableRow>
      </TableBody>
    </Table>
  );
};

SystemInfoRaw.propTypes = {
  info: PropTypes.object.isRequired,
};

const SystemInfo = SystemInfoRaw;
export { SystemInfo };
