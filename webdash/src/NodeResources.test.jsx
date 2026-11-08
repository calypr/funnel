import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { expect, it } from 'vitest';
import { NodeInfo } from './NodeInfo';
import { NodeList } from './Pages';

async function renderNodeList(payload) {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (input) => {
    if (String(input).includes('/v1/nodes')) {
      return {
        ok: true,
        status: 200,
        json: async () => payload,
      };
    }
    return originalFetch(input);
  };

  const container = document.createElement('div');
  const root = createRoot(container);
  await act(async () => {
    root.render(<MemoryRouter><NodeList /></MemoryRouter>);
    await new Promise(resolve => setTimeout(resolve, 0));
  });
  return {
    container,
    unmount: () => {
      act(() => root.unmount());
      globalThis.fetch = originalFetch;
    },
  };
}

function rowValues(table, label) {
  const row = [...table.querySelectorAll('tbody tr')].find(candidate => candidate.cells[0]?.textContent === label);
  return row && [...row.cells].map(cell => cell.textContent.trim());
}

it('summarizes gateway node resources from their serialized snake_case fields', async () => {
  const payload = JSON.parse(`{
    "nodes": [{
      "id": "active",
      "state": "ALIVE",
      "resources": {"cpus": 4, "ram_gb": 8, "disk_gb": 16},
      "available": {"cpus": 2, "ram_gb": 3, "disk_gb": 5}
    }]
  }`);
  const { container, unmount } = await renderNodeList(payload);

  try {
    const table = container.querySelector('[aria-label="Resources on active nodes"]');
    expect(rowValues(table, 'CPU')).toEqual(['CPU', '2 cores', '4 cores']);
    expect(rowValues(table, 'RAM')).toEqual(['RAM', '3 GB', '8 GB']);
    expect(rowValues(table, 'Disk')).toEqual(['Disk', '5 GB', '16 GB']);
  } finally {
    unmount();
  }
});

it('preserves camelCase resources, zero values, and unavailable negative totals', async () => {
  const payload = {
    nodes: [{
      id: 'legacy',
      state: 'ALIVE',
      resources: { cpus: 0, ramGb: -1, diskGb: 0 },
      available: { cpus: 0, ramGb: 0, diskGb: 0 },
    }],
  };
  const { container, unmount } = await renderNodeList(payload);

  try {
    const table = container.querySelector('[aria-label="Resources on active nodes"]');
    expect(rowValues(table, 'CPU')).toEqual(['CPU', '0 cores', '0 cores']);
    expect(rowValues(table, 'RAM')).toEqual(['RAM', '0 GB', 'Unavailable']);
    expect(rowValues(table, 'Disk')).toEqual(['Disk', '0 GB', '0 GB']);
  } finally {
    unmount();
  }
});

it('shows gateway resource and available values in node details', async () => {
  const node = JSON.parse(`{
    "id": "active",
    "resources": {"cpus": 4, "ram_gb": 8, "disk_gb": 16},
    "available": {"cpus": 2, "ram_gb": 3, "disk_gb": 5}
  }`);
  const container = document.createElement('div');
  const root = createRoot(container);

  try {
    await act(async () => root.render(<NodeInfo node={node} />));
    const rows = [...container.querySelectorAll('tr')];
    const resourceRow = rows.find(row => row.cells[0]?.textContent === 'Resources');
    const availableRow = rows.find(row => row.cells[0]?.textContent === 'Available');
    expect(resourceRow?.cells[1]?.textContent).toBe('4 CPU cores, 8 GB RAM, 16 GB disk space');
    expect(availableRow?.cells[1]?.textContent).toBe('2 CPU cores, 3 GB RAM, 5 GB disk space');
  } finally {
    act(() => root.unmount());
  }
});

it('keeps camelCase resource fields in node details', async () => {
  const node = {
    id: 'legacy',
    resources: { cpus: 4, ramGb: 8, diskGb: 16 },
    available: { cpus: 2, ramGb: 3, diskGb: 5 },
  };
  const container = document.createElement('div');
  const root = createRoot(container);

  try {
    await act(async () => root.render(<NodeInfo node={node} />));
    const rows = [...container.querySelectorAll('tr')];
    const resourceRow = rows.find(row => row.cells[0]?.textContent === 'Resources');
    const availableRow = rows.find(row => row.cells[0]?.textContent === 'Available');
    expect(resourceRow?.cells[1]?.textContent).toBe('4 CPU cores, 8 GB RAM, 16 GB disk space');
    expect(availableRow?.cells[1]?.textContent).toBe('2 CPU cores, 3 GB RAM, 5 GB disk space');
  } finally {
    act(() => root.unmount());
  }
});
