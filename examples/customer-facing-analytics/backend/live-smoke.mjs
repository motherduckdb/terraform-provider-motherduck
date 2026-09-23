// Read private Terraform outputs supplied by the lifecycle harness.
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import assert from 'node:assert/strict';
import pg from 'pg';
import { createApp } from './server.mjs';
const outputs = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const tenants = Object.fromEntries(Object.entries(outputs.reader_tokens.value).map(([id, token]) => [id, { token, enabled: !outputs.tenants.value[id].suspended }]));
const sessions = Object.fromEntries(Object.keys(tenants).map((id) => [createHash('sha256').update(`local-test-${id}`).digest('hex'), id]));
const host = process.env.MOTHERDUCK_PG_HOST ?? 'pg.us-east-1-aws.motherduck.com';
const app = createApp({ tenants, sessions, host });
await new Promise((resolve) => app.server.listen(0, '127.0.0.1', resolve));
try {
  const get = (id, extra = '') => fetch(`http://127.0.0.1:${app.server.address().port}/usage?start=2026-01-01&end=2026-01-02${extra}`, { headers: { Authorization: `Bearer local-test-${id}` } });
  for (const [id, count] of [['acme', '42'], ['globex', '17']]) {
    let body;
    for (let attempt = 0; attempt < 30; attempt++) {
      const response = await get(id);
      body = await response.json();
      if (response.status === 200 && body.rows.some((row) => row.event_count === count)) break;
      await new Promise((resolve) => setTimeout(resolve, 2000));
    }
    assert.equal(body.tenant, id);
    assert.equal(body.rows.length, 1);
    assert.equal(body.rows[0].event_count, count);
    assert.equal((await get(id, `&tenant=${id === 'acme' ? 'globex' : 'acme'}`)).status, 403);
    const client = new pg.Client({ host, port: 5432, database: 'reporting', user: 'user', password: tenants[id].token, ssl: { rejectUnauthorized: true }, connectionTimeoutMillis: 10000 });
    try {
      await client.connect();
      await assert.rejects(client.query("INSERT INTO app.daily_usage VALUES (DATE '2026-01-01', 999)"), /read.only|permission|read.scaling/i);
    } finally { await client.end(); }
  }
  console.log('PASS: real tenant backend, isolated sample rows, forged tenant rejection, read-only credentials');
} finally { await app.close(); }
