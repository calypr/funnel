import React from 'react';
import PropTypes from 'prop-types';
import ListItemButton from '@mui/material/ListItemButton';
import ListItemText from '@mui/material/ListItemText';
import Button from '@mui/material/Button';
import MenuItem from '@mui/material/MenuItem';
import Select from '@mui/material/Select';
import TextField from '@mui/material/TextField';
import IconButton from '@mui/material/IconButton';
import DeleteIcon from '@mui/icons-material/Delete';
import AddIcon from '@mui/icons-material/Add';
import AssignmentOutlinedIcon from '@mui/icons-material/AssignmentOutlined';
import DnsOutlinedIcon from '@mui/icons-material/DnsOutlined';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { NavLink } from "react-router-dom";

function ListItemLink(props) {
  return <ListItemButton component={NavLink} className="dashboard-nav-item" {...props} />;
}

const NavListItems = (
  <div>
    <div className="sidebar-section-label">Workspace</div>
    <ListItemLink to="/tasks">
      <AssignmentOutlinedIcon fontSize="small" />
      <ListItemText primary="Tasks" />
    </ListItemLink>
    <ListItemLink to="/nodes">
      <DnsOutlinedIcon fontSize="small" />
      <ListItemText primary="Nodes" />
    </ListItemLink>
    <ListItemLink to="/service-info">
      <InfoOutlinedIcon fontSize="small" />
      <ListItemText primary="Service Info" />
    </ListItemLink>
  </div>
);

function TaskFilters({show, stateFilter, tagsFilter, setStateFilter, setTagsFilter}) {

  const updateTagFilter = (index, newTagValue) => {
    let tags =[...tagsFilter];
    tags[index] = newTagValue;
    setTagsFilter(tags);
  };

  const addTagFilter = () => {
    const tags = tagsFilter.concat({key: "", value: ""});
    setTagsFilter(tags);
  };

  const removeTagFilter = (index) => {
    const tags = tagsFilter.filter((item, j) => index !== j);
    setTagsFilter(tags);
  };

  if (show === false) {
    return (<div />);
  };
  return (
    <div className="sidebar-filters">
      <div className="sidebar-section-label">Task filters</div>
      <label className="filter-label" htmlFor="state-placeholder">State</label>
        <Select
          value={stateFilter}
          onChange={(event) => setStateFilter(event.target.value)}
          id="state-placeholder"
          size="small"
          fullWidth
          inputProps={{ 'aria-label': 'Task state' }}
          displayEmpty
          name="stateFilter"
        >
          <MenuItem value="">
            All states
          </MenuItem>
          <MenuItem value="QUEUED">Queued</MenuItem>
          <MenuItem value="INITIALIZING">Initializing</MenuItem>
          <MenuItem value="RUNNING">Running</MenuItem>
          <MenuItem value="COMPLETE">Complete</MenuItem>
          <MenuItem value="CANCELED">Canceled</MenuItem>
          <MenuItem value="EXECUTOR_ERROR">Executor Error</MenuItem>
          <MenuItem value="SYSTEM_ERROR">System error</MenuItem>
        </Select>
      <div className="tag-filter-heading">
        <span className="filter-label">Tags</span>
        <Button onClick={addTagFilter} startIcon={<AddIcon />} size="small" aria-label="Add tag filter">Add tag</Button>
      </div>
      {tagsFilter.map((tag, index) => (
        <div className="tag-filter-row" key={index}>
          <TagFilter index={index} tag={tag} updateTagFilter={updateTagFilter} />
          <IconButton onClick={() => removeTagFilter(index)} aria-label="Remove tag filter">
            <DeleteIcon fontSize="small" />
          </IconButton>
        </div>
      ))}
    </div>
  );
};

TaskFilters.propTypes = {
  show: PropTypes.bool.isRequired,
  stateFilter: PropTypes.string.isRequired,
  tagsFilter: PropTypes.array.isRequired,
  setStateFilter: PropTypes.func.isRequired,
  setTagsFilter: PropTypes.func.isRequired,
};

const useDebounce = (value, delay) => {
  const [debouncedValue, setDebouncedValue] = React.useState(value);

  React.useEffect(() => {
    const handler = setTimeout(() => {
      setDebouncedValue(value);
    }, delay);
    
    return () => {
      clearTimeout(handler);
    };
  }, [value, delay]);
  
  return debouncedValue;
};

function TagFilter({tag, index, updateTagFilter}) {
  const [key, setKey] = React.useState(tag.key);
  const [value, setValue] = React.useState(tag.value);
  const debouncedKey = useDebounce(key, 500);
  const debouncedValue = useDebounce(value, 500);

  React.useEffect(() => {
    updateTagFilter(index, {"key": debouncedKey, "value": debouncedValue});
  }, [debouncedKey, debouncedValue, index]);

  return(
    <div className="tag-filter-fields">
      <div className="tag-inputs">
          <TextField 
            label="Key"
            placeholder=""
            value={key}
            margin="normal"
            size="small"
            fullWidth
            style={{marginTop:"0px"}}
            onChange={(event) => setKey(event.target.value)}
          />
          <TextField 
            label="Value"
            placeholder=""
            value={value}
            margin="normal"
            size="small"
            fullWidth
            style={{marginTop:"0px"}}
            onChange={(event) => setValue(event.target.value)}
          />
      </div>
    </div>
  );
};

TagFilter.propTypes = {
  tag: PropTypes.object.isRequired,
  index: PropTypes.number.isRequired,
  updateTagFilter: PropTypes.func.isRequired,
};

export {NavListItems, TaskFilters};
