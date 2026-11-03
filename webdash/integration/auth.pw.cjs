const { test, expect } = require('@playwright/test');
const { spawn, execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');

let server;
let tempDir;
let address;

async function freePort() {
  const socket = net.createServer();
  await new Promise((resolve, reject) => socket.listen(0, '127.0.0.1', resolve).once('error', reject));
  const port = socket.address().port;
  await new Promise(resolve => socket.close(resolve));
  return port;
}

test.beforeAll(async () => {
  tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'funnel-browser-test-'));
  const binary = process.env.FUNNEL_TEST_BINARY || path.join(tempDir, 'funnel');
  if (!process.env.FUNNEL_TEST_BINARY) {
    console.log('Building Funnel for the browser integration test...');
    execFileSync('go', ['build', '-o', binary, '.'], { cwd: path.resolve(__dirname, '../..'), stdio: 'inherit' });
  }
  const httpPort = await freePort();
  const rpcPort = await freePort();
  address = `http://127.0.0.1:${httpPort}`;
  const config = path.join(tempDir, 'config.yaml');
  fs.writeFileSync(config, `Compute: noop\nBoltDB:\n  Path: ${tempDir}/funnel.db\nServer:\n  HostName: 127.0.0.1\n  HTTPPort: "${httpPort}"\n  RPCPort: "${rpcPort}"\n  BasicAuth:\n    - User: test-user\n      Password: test-password\n`);
  console.log('Starting Funnel with temporary basic-auth credentials...');
  server = spawn(binary, ['server', 'run', '--config', config], { cwd: tempDir, stdio: 'pipe' });
  let output = '';
  server.stdout.on('data', data => { output += data; });
  server.stderr.on('data', data => { output += data; });
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    if (server.exitCode !== null) throw new Error(`Funnel exited during startup: ${output}`);
    try {
      const response = await fetch(`${address}/healthz`);
      if (response.ok) {
        console.log('Funnel is ready. Testing dashboard sign-in...');
        return;
      }
    } catch (_) {}
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw new Error(`Funnel did not start: ${output}`);
});

test.afterAll(async () => {
  if (server && server.exitCode === null) {
    server.kill('SIGTERM');
    await new Promise(resolve => server.once('exit', resolve));
  }
  if (tempDir) fs.rmSync(tempDir, { recursive: true, force: true });
});

test('signs in from the dashboard and loads tasks', async ({ browser }) => {
  const anonymous = await browser.newContext();
  const anonymousPage = await anonymous.newPage();
  await anonymousPage.goto(`${address}/tasks`);
  await expect(anonymousPage.getByText('Sign in required.')).toBeVisible();
  const signIn = anonymousPage.getByRole('link', { name: 'Sign in', exact: true });
  await expect(signIn).toHaveAttribute('href', '/login');
  const challenge = await anonymous.request.get(`${address}/login`, { maxRedirects: 0 });
  expect(challenge.status()).toBe(401);
  expect(challenge.headers()['www-authenticate']).toContain('Basic');
  await anonymous.setHTTPCredentials({ username: 'test-user', password: 'test-password' });
  const taskResponse = anonymousPage.waitForResponse(response => response.url().includes('/v1/tasks') && response.status() === 200);
  await signIn.click();
  await taskResponse;
  await expect(anonymousPage.getByRole('heading', { name: 'Tasks' })).toBeVisible();
  await expect(anonymousPage.getByText('Sign in required.')).toHaveCount(0);
  await anonymous.close();
});

test('summarizes resources for active nodes', async ({ browser }) => {
  const context = await browser.newContext({ httpCredentials: { username: 'test-user', password: 'test-password' } });
  const page = await context.newPage();
  let nodes = [
    { id: 'active-1', state: 'ALIVE', resources: { cpus: 4, ramGb: 8, diskGb: 20 }, available: { cpus: 2, ramGb: 3, diskGb: 10 } },
    { id: 'active-2', state: 'ALIVE', resources: { cpus: 2, ramGb: 4, diskGb: 10 }, available: { cpus: 1, ramGb: 2, diskGb: 5 } },
    { id: 'dead', state: 'DEAD', resources: { cpus: 100, ramGb: 100, diskGb: 100 }, available: { cpus: 100, ramGb: 100, diskGb: 100 } },
    { id: 'draining', state: 'DRAIN', resources: { cpus: 100 }, available: { cpus: 100 } },
  ];
  await page.route('**/v1/nodes**', route => route.fulfill({ json: { nodes } }));
  await page.goto(`${address}/nodes`);
  const summary = page.getByRole('table', { name: 'Resources on active nodes' });
  await expect(summary.getByRole('row', { name: 'CPU 3 cores 6 cores' })).toBeVisible();
  await expect(summary.getByRole('row', { name: 'RAM 5 GB 12 GB' })).toBeVisible();
  await expect(summary.getByRole('row', { name: 'Disk 15 GB 30 GB' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'dead', exact: true })).toBeVisible();

  nodes = [{ id: 'incomplete', state: 'ALIVE', resources: { cpus: 2 }, available: null }];
  await page.reload();
  await expect(summary.getByRole('row', { name: 'CPU Unavailable 2 cores' })).toBeVisible();
  await expect(summary.getByRole('row', { name: 'RAM Unavailable 0 GB' })).toBeVisible();

  nodes = [];
  await page.reload();
  await expect(page.getByText('No active nodes')).toBeVisible();
  await expect(summary).toHaveCount(0);
  await context.close();
});
