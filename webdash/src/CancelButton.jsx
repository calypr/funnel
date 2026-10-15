import React from 'react';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogContentText from '@mui/material/DialogContentText';
import DialogTitle from '@mui/material/DialogTitle';

import { isDone, post } from './utils';

function CancelButton({task}) {
  const [open, setOpen] = React.useState(false);

  const cancelTask = () => {
    var url = new URL("/v1/tasks/" + task.id + ":cancel", window.location.origin);
    post(url).catch((error) =>
      console.log("cancelTask", url.toString(), "error:", error)
    );
  };

  const handleClickOpen = () => {
    setOpen(true);
  };

  const handleNo = () => {
    setOpen(false);
  };

  const handleYes = () => {
    cancelTask(task.id);
    setOpen(false);
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
          </DialogContent>
          <DialogActions>
            <Button onClick={handleNo} color="primary">
              No
            </Button>
            <Button onClick={handleYes} color="primary" autoFocus>
              Yes
            </Button>
          </DialogActions>
        </Dialog>
      </div>
    );
  }
}

export { CancelButton };
