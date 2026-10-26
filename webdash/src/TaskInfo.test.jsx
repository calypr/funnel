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
