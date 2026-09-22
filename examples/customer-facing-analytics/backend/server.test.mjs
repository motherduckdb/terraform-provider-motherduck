import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createApp } from './server.mjs';

const hash = (s) => createHash('sha256').update(s).digest('hex');
test('authenticated tenants select isolated pools, values are bound, suspension denies access', async () => {
  const configs = [];
  const queries = [];
  let closed = 0;
  const app = createApp({
    host: 'pg.us-east-1-aws.motherduck.com',
    tenants: { acme: { token: 'acme-reader', enabled: true }, globex: { token: 'globex-reader', enabled: true }, paused: { enabled: false } },
    sessions: { [hash('a')]: 'acme', [hash('g')]: 'globex', [hash('p')]: 'paused' },
    poolFactory(config) {
      configs.push(config);
      return { on() {}, async query(sql, args) { queries.push({ sql, args }); return { rows: [{ event_count: config.password === 'acme-reader' ? '42' : '17' }] }; }, async end() { closed++; } };
    },
  });
  await new Promise((resolve) => app.server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${app.server.address().port}/usage?start=2026-01-01&end=2026-01-02`;
  try {
    const get = (session, suffix = '') => fetch(url + suffix, { headers: { Authorization: `Bearer ${session}` } });
    assert.equal((await (await get('a')).json()).rows[0].event_count, '42');
    assert.equal((await (await get('g')).json()).rows[0].event_count, '17');
    assert.equal((await get('a', '&tenant=globex')).status, 403);
    assert.equal((await get('p')).status, 403);
    assert.equal((await get('unknown')).status, 403);
    assert.equal((await get('a', '&start=2026-01-03')).status, 400);
    assert.equal((await get('a', '&sql=DROP')).status, 400);
    assert.equal(configs.length, 2);
    assert.equal(configs[0].ssl.rejectUnauthorized, true);
    assert.notEqual(configs[0].password, configs[1].password);
    assert.deepEqual(queries[0].args, ['2026-01-01', '2026-01-02']);
    assert.match(queries[0].sql, /\$1::DATE/);
  } finally { await app.close(); }
  assert.equal(closed, 2);
});
