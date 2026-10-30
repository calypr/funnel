import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { afterEach, expect, it, vi } from 'vitest';
import App from './App';

let root;

afterEach(() => {
  if (root) {
    act(() => root.unmount());
    root = undefined;
  }
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

it('shows final stderr without refreshing a running task page', async () => {
  vi.useFakeTimers();
  window.history.replaceState({}, '', '/tasks/task-539');
  const running = {
    id: 'task-539',
    state: 'RUNNING',
    executors: [{ image: 'alpine', command: ['example', '--invalid'] }],
  };
  const failed = {
    ...running,
    state: 'EXECUTOR_ERROR',
    logs: [{ logs: [{ exit_code: 1, stderr: 'unknown option: --invalid' }] }],
  };
  let currentTask = running;
  const fetchTask = vi.fn(async () => ({ status: 200, json: async () => currentTask }));
  vi.stubGlobal('fetch', fetchTask);
  const container = document.createElement('div');
  root = createRoot(container);

  await act(async () => root.render(<App />));
  expect(container.textContent).toContain('RUNNING');
  expect(container.textContent).not.toContain('unknown option: --invalid');

  currentTask = failed;
  await act(async () => vi.advanceTimersByTimeAsync(10000));

  expect(container.textContent).toContain('EXECUTOR_ERROR');
  expect(container.textContent).toContain('unknown option: --invalid');
  const requestsAtCompletion = fetchTask.mock.calls.length;
  await act(async () => vi.advanceTimersByTimeAsync(30000));
  expect(fetchTask).toHaveBeenCalledTimes(requestsAtCompletion);
});

it('stops refreshing task details after leaving the page', async () => {
  vi.useFakeTimers();
  window.history.replaceState({}, '', '/tasks/task-539');
  const fetchTask = vi.fn(async () => ({
    status: 200,
    json: async () => ({ id: 'task-539', state: 'RUNNING' }),
  }));
  vi.stubGlobal('fetch', fetchTask);
  root = createRoot(document.createElement('div'));
  await act(async () => root.render(<App />));
  act(() => root.unmount());
  root = undefined;
  const requestsAtUnmount = fetchTask.mock.calls.length;

  await act(async () => vi.advanceTimersByTimeAsync(30000));

  expect(fetchTask).toHaveBeenCalledTimes(requestsAtUnmount);
});

it.each([
  [401, 'Sign in required'],
  [403, 'You do not have permission to access this resource'],
])('shows an access error for HTTP %s', async (status, message) => {
  window.history.replaceState({}, '', '/tasks');
  vi.stubGlobal('fetch', async () => ({ status, json: async () => ({ error: 'denied' }) }));
  const container = document.createElement('div');
  root = createRoot(container);

  await act(async () => {
    root.render(<App />);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

  expect(container.textContent).toContain(message);
  if (status === 401) {
    expect(container.querySelector('a[href="/login"]')?.textContent).toBe('Sign in');
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

it('shows tasks without an access error when the request succeeds', async () => {
  window.history.replaceState({}, '', '/tasks');
  vi.stubGlobal('fetch', async () => ({ status: 200, json: async () => ({ tasks: [{ id: 'task-123', state: 'COMPLETE', name: 'Example task' }] }) }));
  const container = document.createElement('div');
  root = createRoot(container);

  await act(async () => {
    root.render(<App />);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

  expect(container.textContent).toContain('task-123');
  expect(container.textContent).toContain('Example task');
  expect(container.querySelector('[role="alert"]')).toBeNull();
});

it('loads nodes from every page', async () => {
  window.history.replaceState({}, '', '/nodes');
  const requests = [];
  vi.stubGlobal('fetch', async (input) => {
    const url = new URL(input);
    requests.push(url);
    const pageToken = url.searchParams.get('page_token');
    if (!pageToken) {
      return {
        status: 200,
        json: async () => ({
          nodes: [{ id: 'node-first', hostname: 'first', state: 'DEAD' }],
          next_page_token: 'page-two',
        }),
      };
    }
    return {
      status: 200,
      json: async () => ({
        nodes: [{ id: 'node-second', hostname: 'second', state: 'DEAD' }],
      }),
    };
  });
  const container = document.createElement('div');
  root = createRoot(container);

  await act(async () => {
    root.render(<App />);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

  expect(container.querySelector('a[href="/nodes/node-first"]')).not.toBeNull();
  expect(container.querySelector('a[href="/nodes/node-second"]')).not.toBeNull();
  expect(requests).toHaveLength(2);
  expect(requests[0].searchParams.get('page_size')).toBe('1000');
  expect(requests[1].searchParams.get('page_token')).toBe('page-two');
});

it('does not display an incomplete node list when a later page fails', async () => {
  window.history.replaceState({}, '', '/nodes');
  vi.stubGlobal('fetch', async (input) => {
    const url = new URL(input);
    if (!url.searchParams.has('page_token')) {
      return {
        status: 200,
        json: async () => ({
          nodes: [{ id: 'node-first', hostname: 'first', state: 'DEAD' }],
          next_page_token: 'page-two',
        }),
      };
    }
    return { status: 500, json: async () => ({ error: 'backend unavailable' }) };
  });
  const container = document.createElement('div');
  root = createRoot(container);

  await act(async () => {
    root.render(<App />);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

  expect(container.textContent).toContain('Could not load nodes.');
  expect(container.querySelector('a[href="/nodes/node-first"]')).toBeNull();
});

it('requests BASIC task lists and FULL task details', async () => {
  const requests = [];
  vi.stubGlobal('fetch', async (input) => {
    const url = new URL(input);
    requests.push(url);
    if (url.pathname === '/v1/tasks') {
      return { status: 200, json: async () => ({ tasks: [{ id: 'task-view', state: 'COMPLETE', name: 'Example' }] }) };
    }
    return { status: 200, json: async () => ({ id: 'task-view', state: 'COMPLETE', logs: [] }) };
  });
  const container = document.createElement('div');
  root = createRoot(container);

  window.history.replaceState({}, '', '/tasks');
  await act(async () => {
    root.render(<App />);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  expect(requests[0].pathname).toBe('/v1/tasks');
  expect(requests[0].searchParams.get('view')).toBe('BASIC');

  act(() => root.unmount());
  window.history.replaceState({}, '', '/tasks/task-view');
  const detailRoot = createRoot(container);
  root = detailRoot;
  await act(async () => {
    detailRoot.render(<App />);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  expect(requests.some(url => url.pathname === '/v1/tasks/task-view' && url.searchParams.get('view') === 'FULL')).toBe(true);
});
