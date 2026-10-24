import React from 'react';
import { BrowserRouter as Router } from "react-router-dom";

import './App.css';
import Dashboard from './Dashboard';

function App() {
  return (
    <Router>
      <Dashboard />
    </Router>
  );
};

export default App;
