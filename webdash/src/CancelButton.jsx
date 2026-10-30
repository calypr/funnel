import React from 'react';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogContentText from '@mui/material/DialogContentText';
import DialogTitle from '@mui/material/DialogTitle';

import { isDone, post } from './utils';

function CancelButton({task, onCanceled}) {
  const [open, setOpen] = React.useState(false);
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState('');

  const handleClickOpen = () => {
    setError('');
    setOpen(true);
  };

  const handleNo = () => {
    setOpen(false);
  };

  const handleYes = async () => {
    setPending(true);
    setError('');
    try {
      const url = new URL('/v1/tasks/' + encodeURIComponent(task.id) + ':cancel', window.location.origin);
      await post(url);
      setOpen(false);
      if (onCanceled) onCanceled();
    } catch (err) {
      setError(`Cancellation failed: ${err.message}`);
    } finally {
      setPending(false);
    }
  };

  if (task.state === undefined || isDone(task)) {
    return (<div />);
  } else {
    return (
      <div>
        <Button variant="outlined" onClick={handleClickOpen} size="small">
          Cancel
        </Button>
        <Dialog
          open={open}
          onClose={null}
          aria-labelledby="alert-dialog-title"
          aria-describedby="alert-dialog-description"
        >
          <DialogTitle id="alert-dialog-title">Cancel task: {task.id}?</DialogTitle>
          <DialogContent>
            <DialogContentText id="alert-dialog-description">
              This action can not be undone.
            </DialogContentText>
            {error && <DialogContentText role="alert" color="error">{error}</DialogContentText>}
          </DialogContent>
          <DialogActions>
            <Button onClick={handleNo} color="primary" disabled={pending}>
              No
            </Button>
            <Button onClick={handleYes} color="primary" autoFocus disabled={pending}>
              {pending ? 'Canceling...' : 'Yes'}
            </Button>
          </DialogActions>
        </Dialog>
      </div>
    );
  }
}

export { CancelButton };
