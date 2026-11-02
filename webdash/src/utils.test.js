import { beforeEach, afterEach, expect, it, vi } from 'vitest';

let api;

beforeEach(async () => {
  vi.resetModules();
  vi.stubGlobal('fetch', vi.fn(async input => String(input) === '/login/token'
    ? { status: 200, text: async () => 'test-token' }
    : { status: 200, ok: true, json: async () => ({}) }));
  api = await import('./utils');
});

afterEach(() => vi.unstubAllGlobals());

it.each([
  'https://other.invalid/v1/tasks',
  '/login/token',
  '/v1/tasks/../../login',
  '/v1/tasks/task/../..',
])('rejects unintended request destinations: %s', address => {
  expect(() => api.get(new URL(address, window.location.origin))).toThrow('supported same-origin API');
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('rejects strings and POSTs to read-only API endpoints', () => {
  expect(() => api.get('/v1/tasks')).toThrow('Expected URL object');
  expect(() => api.post(new URL('/v1/nodes', window.location.origin))).toThrow('supported same-origin API');
  expect(fetch).toHaveBeenCalledTimes(1);
});

it('preserves task filters, bearer authentication, and cancellation', async () => {
  const url = new URL('/v1/tasks?page_size=10&name_prefix=example', window.location.origin);
  await api.get(url);
  const [target, options] = fetch.mock.calls[1];
  expect(new URL(target).searchParams.get('name_prefix')).toBe('example');
  expect(new URL(target).searchParams.get('view')).toBe('FULL');
  expect(options.headers.Authorization).toBe('Bearer test-token');
  expect(options.mode).toBe('same-origin');
  expect(options.redirect).toBe('error');
  await api.post(new URL('/v1/tasks/task-1:cancel', window.location.origin));
  expect(fetch.mock.calls[2][1].method).toBe('POST');
});
