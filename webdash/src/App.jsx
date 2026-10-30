import React from 'react';
import { BrowserRouter as Router } from "react-router-dom";
import { createTheme, ThemeProvider } from '@mui/material/styles';

import './App.css';
import Dashboard from './Dashboard';

const theme = createTheme({ palette: { primary: { main: '#333333' } } });

function App() {
  return (
    <ThemeProvider theme={theme}>
      <Router>
        <Dashboard />
      </Router>
    </ThemeProvider>
  );
};

export default App;
