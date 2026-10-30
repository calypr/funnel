import React from 'react';
import PropTypes from 'prop-types';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableRow from '@mui/material/TableRow';

function SystemInfo({ info }) {
  const fields = [
    ['ID', info.id],
    ['Version', info.version],
    ['Description', info.description],
    ['Organization', info.organization?.name],
    ['Organization URL', info.organization?.url],
    ['Contact URL', info.contactUrl],
    ['Documentation URL', info.documentationUrl],
    ['Created at', info.createdAt],
    ['Updated at', info.updatedAt],
    ['Storage', info.storage],
    ['Backend parameters', info.tesResources_backend_parameter],
    ['Type artifact', info.type?.artifact],
    ['Type group', info.type?.group],
    ['Type version', info.type?.version],
  ];

  return (
    <Table className="info-table" aria-label="Service information">
      <TableBody>
        {fields.filter(([, value]) => value != null && value !== '').map(([label, value]) => (
          <TableRow key={label}>
            <TableCell component="th" scope="row">{label}</TableCell>
            <TableCell>{typeof value === 'object' ? JSON.stringify(value) : String(value)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

SystemInfo.propTypes = { info: PropTypes.object.isRequired };

export { SystemInfo };
