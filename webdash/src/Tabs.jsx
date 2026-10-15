import React from 'react';
import PropTypes from 'prop-types';
import Tabs from '@mui/material/Tabs';
import Tab from '@mui/material/Tab';
import Typography from '@mui/material/Typography';
import Box from '@mui/material/Box';
import Paper from '@mui/material/Paper';

function TabPanel(props) {
  const { children, value, index, ...other } = props;

  return (
    <Typography
      component="div"
      role="tabpanel"
      hidden={value !== index}
      id={`simple-tabpanel-${index}`}
      aria-labelledby={`simple-tab-${index}`}
      {...other}
    >
      {value === index && <Box p={3}>{children}</Box>}
    </Typography>
  );
}

TabPanel.propTypes = {
  children: PropTypes.node,
  index: PropTypes.any.isRequired,
  value: PropTypes.any.isRequired,
};

function a11yProps(index) {
  return {
    id: `simple-tab-${index}`,
    'aria-controls': `simple-tabpanel-${index}`,
  };
}

function SimpleTabs(header, textComponent, jsonComponent) {
  const [value, setValue] = React.useState(0);

  const handleChange = (event, newValue) => {
    setValue(newValue);
  };

  return (
    <div>
      {header}
      <Paper style={{minWidth: "250px", width: "100%", overflowX: "auto"}}>
        <Tabs value={value} onChange={handleChange} aria-label="simple tabs">
          <Tab label="TEXT" {...a11yProps(0)} />
          <Tab label="JSON" {...a11yProps(1)} />
        </Tabs>
        <TabPanel value={value} index={0}>
          {textComponent}
        </TabPanel>
        <TabPanel value={value} index={1}>
          {jsonComponent}
        </TabPanel>
      </Paper>
    </div>
  );
}

export { SimpleTabs };
