import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, it } from 'vitest';
import { TaskInfo } from './TaskInfo';

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
  const root = createRoot(container);
  const error = 'could not find matching storage\nswift: authentication failed';
  const log = `level='error' msg='System error' error='${error}'`;

  await act(async () => {
    root.render(<TaskInfo task={{ id: 'task-1', logs: [{ system_logs: [log] }] }} />);
  });

  const cell = [...container.querySelectorAll('td')].find(element => element.textContent === error);
  expect(cell).toBeDefined();
  expect(getComputedStyle(cell).whiteSpace).toBe('pre-line');
  act(() => root.unmount());
});
