import React from 'react';
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import Box from '@mui/material/Box';
import CssBaseline from '@mui/material/CssBaseline';
import Drawer from '@mui/material/Drawer';
import AppBar from '@mui/material/AppBar';
import Toolbar from '@mui/material/Toolbar';
import List from '@mui/material/List';
import Typography from '@mui/material/Typography';
import Divider from '@mui/material/Divider';
import IconButton from '@mui/material/IconButton';
import MenuIcon from '@mui/icons-material/Menu';
import ChevronLeftIcon from '@mui/icons-material/ChevronLeft';
import Alert from '@mui/material/Alert';

import { NavListItems, TaskFilters } from './DrawerFilters';
import { TaskList, Task, Node, NodeList, ServiceInfo, NoMatch } from './Pages';

const drawerWidth = 260;

function Dashboard() {
  const location = useLocation();
  const [tagsFilter, setTagsFilter] = React.useState([]);
  const [stateFilter, setStateFilter] = React.useState("");
  const [pageToken, setPageToken] = React.useState("");
  const [pageSize, setPageSize] = React.useState(25);
  const [nextPageToken, setNextPageToken] = React.useState("");
  const [prevPageToken, setPrevPageToken] = React.useState([]);
  const [open, setOpen] = React.useState(window.innerWidth > 500);
  const [accessError, setAccessError] = React.useState(null);

  React.useEffect(() => {
    const onAccessError = (event) => setAccessError(event.detail);
    window.addEventListener('funnel-access-error', onAccessError);
    return () => window.removeEventListener('funnel-access-error', onAccessError);
  }, []);

  React.useEffect(() => setAccessError(null), [location.pathname]);

  const handleDrawerOpen = () => {
    setOpen(true);
  };

  const handleDrawerClose = () => {
    setOpen(false);
  };

  //console.log("Dashboard stateFilter:", stateFilter)
  //console.log("Dashboard tagsFilter:", tagsFilter)

  return (
    <Box sx={{ display: 'flex' }}>
      <CssBaseline />
      <AppBar
        position="fixed"
        sx={theme => ({
          backgroundColor: '#ffffff',
          color: '#222222',
          boxShadow: 'none',
          borderBottom: '1px solid #e2e2e2',
          zIndex: theme.zIndex.drawer + 1,
          transition: theme.transitions.create(['width', 'margin'], {
            easing: theme.transitions.easing.sharp,
            duration: open ? theme.transitions.duration.enteringScreen : theme.transitions.duration.leavingScreen,
          }),
          ...(open && {
            marginLeft: `${drawerWidth}px`,
            width: `calc(100% - ${drawerWidth}px)`,
          }),
        })}
      >
        <Toolbar disableGutters={!open} sx={{ paddingRight: '24px' }}>
          <IconButton
            color="inherit"
            aria-label="Open drawer"
            onClick={handleDrawerOpen}
            sx={{
              marginLeft: '12px',
              marginRight: '36px',
              ...(open && { display: 'none' }),
            }}
          >
            <MenuIcon />
          </IconButton>
          <Typography
            component="h1"
            variant="h6"
            color="inherit"
            noWrap
            sx={{ flexGrow: 1 }}
          >
            <Box component="span" className="brand-name">Funnel</Box>
            <Box component="span" className="brand-description">TASK EXECUTION</Box>
          </Typography>
        </Toolbar>
      </AppBar>
      <Drawer
        variant="permanent"
        slotProps={{
          paper: {
            sx: theme => ({
              position: 'relative',
              backgroundColor: '#f8f8f8',
              borderRight: '1px solid #e2e2e2',
              whiteSpace: 'nowrap',
              width: drawerWidth,
              overflowX: 'wrap',
              transition: theme.transitions.create('width', {
                easing: theme.transitions.easing.sharp,
                duration: open ? theme.transitions.duration.enteringScreen : theme.transitions.duration.leavingScreen,
              }),
              ...(!open && { overflowX: 'hidden', width: 0 }),
            }),
          },
        }}
        open={open}
      >
        <Box sx={theme => ({
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'flex-end',
          padding: '0 8px',
          ...theme.mixins.toolbar,
        })}>
          <IconButton onClick={handleDrawerClose} aria-label="Close drawer">
            <ChevronLeftIcon />
          </IconButton>
        </Box>
        <Divider />
        <List>{NavListItems}</List>
        <Divider />
        <TaskFilters
          show={location.pathname.endsWith("tasks") || location.pathname === "/"}
          stateFilter={stateFilter}
          tagsFilter={tagsFilter}
          setStateFilter={setStateFilter}
          setTagsFilter={setTagsFilter}
        />
      </Drawer>
      <Box component="main" sx={theme => ({
        flexGrow: 1,
        minWidth: 0,
        padding: { xs: theme.spacing(2), md: theme.spacing(4) },
        backgroundColor: '#f5f5f5',
        height: '100vh',
        overflow: 'auto',
      })}>
        <Box sx={theme => theme.mixins.toolbar} />
        {accessError && (
          <Alert severity="error" onClose={() => setAccessError(null)}>
            {accessError === 401 ? <>Sign in required. <a href="/login">Sign in</a> and try again.</> : 'You do not have permission to access this resource.'}
          </Alert>
        )}
        <Routes>
          <Route path="/" element={<Navigate to="/tasks" replace />} />
          <Route path="/v1/tasks" element={(
            <TaskList pageSize={pageSize}
                      setPageSize={setPageSize}
                      pageToken={pageToken}
                      setPageToken={setPageToken}
                      nextPageToken={nextPageToken}
                      setNextPageToken={setNextPageToken}
                      prevPageToken={prevPageToken}
                      setPrevPageToken={setPrevPageToken}
                      stateFilter={stateFilter}
                      tagsFilter={tagsFilter} />
          )} />
          <Route path="/tasks" element={(
            <TaskList pageSize={pageSize}
                      setPageSize={setPageSize}
                      pageToken={pageToken}
                      setPageToken={setPageToken}
                      nextPageToken={nextPageToken}
                      setNextPageToken={setNextPageToken}
                      prevPageToken={prevPageToken}
                      setPrevPageToken={setPrevPageToken}
                      stateFilter={stateFilter}
                      tagsFilter={tagsFilter} />
          )} />
          <Route path="/v1/tasks/:task_id" element={<Task />} />
          <Route path="/tasks/:task_id" element={<Task />} />
          <Route path="/v1/nodes" element={<NodeList />} />
          <Route path="/nodes" element={<NodeList />} />
          <Route path="/v1/nodes/:node_id" element={<Node />} />
          <Route path="/nodes/:node_id" element={<Node />} />
          <Route path="/v1/service-info" element={<ServiceInfo />} />
          <Route path="/service-info" element={<ServiceInfo />} />
          <Route path="*" element={<NoMatch />} />
        </Routes>
      </Box>
    </Box>
  );
}

export default Dashboard;
