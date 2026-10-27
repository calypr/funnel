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
  vi.unstubAllGlobals();
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
