import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, expect, it } from 'vitest';
import App from './App';

let root;

afterEach(() => {
  if (root) {
    act(() => root.unmount());
    root = undefined;
  }
});

it('renders the task list route', async () => {
  window.history.replaceState({}, '', '/tasks');
  const container = document.createElement('div');
  root = createRoot(container);

  await act(async () => {
    root.render(<App />);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

  expect(container.textContent).toContain('Tasks');
});
