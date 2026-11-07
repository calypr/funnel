import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it } from 'vitest';
import { TaskInfo } from './TaskInfo';

it('shows a zero exit code for an executor', async () => {
  const container = document.createElement('div');
  const root = createRoot(container);
  const task = {
    id: 'task-1',
    executors: [{ command: ['true'], image: 'alpine' }],
    logs: [{ logs: [{ exit_code: 0 }] }],
    outputs: [{ path: '/output', type: 'FILE' }],
  };
  await act(async () => root.render(<TaskInfo task={task} />));
  const rows = [...container.querySelectorAll('tr')];
  const exitRow = rows.find(row => row.cells[0]?.textContent === 'Exit Code');
  expect(exitRow?.cells[1]?.textContent).toBe('0');
  expect(container.textContent).toContain('FILE');
  act(() => root.unmount());
});

it('renders resources from a gateway-shaped task payload', async () => {
  const task = JSON.parse(`{
    "id": "qa",
    "resources": {
      "backend_parameters": {},
      "backend_parameters_strict": false,
      "cpu_cores": 4,
      "disk_gb": 16,
      "preemptible": false,
      "ram_gb": 8,
      "zones": []
    }
  }`);
  const container = document.createElement('div');
  const root = createRoot(container);

  try {
    await act(async () => root.render(<TaskInfo task={task} />));
    const row = [...container.querySelectorAll('tr')].find(row => row.cells[0]?.textContent === 'Resources');
    expect(row?.cells[1]?.textContent).toBe('4 CPU cores, 8 GB RAM, 16 GB disk space');
  } finally {
    act(() => root.unmount());
  }
});

it('keeps camelCase resource support and renders zero or negative numeric values', async () => {
  const container = document.createElement('div');
  const root = createRoot(container);

  try {
    await act(async () => root.render(
      <TaskInfo task={{
        id: 'task-1',
        resources: { cpuCores: 0, ramGb: -1, diskGb: 0 },
      }} />,
    ));
    const row = [...container.querySelectorAll('tr')].find(row => row.cells[0]?.textContent === 'Resources');
    expect(row?.cells[1]?.textContent).toBe('0 CPU cores, -1 GB RAM, 0 GB disk space');
  } finally {
    act(() => root.unmount());
  }
});

it.each(['system_logs', 'systemLogs'])('shows complete commands from %s', async (field) => {
  const container = document.createElement('div');
  const root = createRoot(container);
  const command = "docker run -e HOME=/tmp/work -e VALUE=a=b it's ready";
  const log = "level='info' msg='Running command' cmd='docker run -e HOME=/tmp/work -e VALUE=a=b it\\'s ready'";

  await act(async () => {
    root.render(<TaskInfo task={{ id: 'task-1', logs: [{ [field]: [log] }] }} />);
  });

  expect(container.textContent).toContain('Running command');
  expect(container.textContent).toContain(command);
  act(() => root.unmount());
});

it('preserves line breaks in system log errors', async () => {
  const container = document.createElement('div');
  document.body.append(container);
  const root = createRoot(container);
  const error = 'could not find matching storage\nswift: authentication failed';
  const log = `level='error' msg='System error' error='${error}'`;

  try {
    await act(async () => {
      root.render(<TaskInfo task={{ id: 'task-1', logs: [{ system_logs: [log] }] }} />);
    });
    const cell = [...container.querySelectorAll('td')].find(element => element.textContent === error);
    expect(cell).toBeDefined();
    expect(getComputedStyle(cell).whiteSpace).toBe('pre-line');
  } finally {
    act(() => root.unmount());
    container.remove();
  }
});

it.each([
  ['missing', undefined],
  ['null', null],
  ['empty', []],
  ['no executor logs', [{}]],
])('renders a SYSTEM_ERROR task with %s logs', async (_name, logs) => {
  const container = document.createElement('div');
  const root = createRoot(container);
  const task = {
    id: 'failed-before-execution',
    state: 'SYSTEM_ERROR',
    executors: [{ command: ['echo', 'hello'], image: 'alpine' }],
    logs,
  };

  try {
    await act(async () => root.render(<TaskInfo task={task} />));
    expect(container.textContent).toContain('SYSTEM_ERROR');
    expect(container.textContent).toContain('echo hello');
    expect(container.textContent).toContain('alpine');
    expect(container.textContent).not.toContain('Exit Code');
  } finally {
    act(() => root.unmount());
  }
});
