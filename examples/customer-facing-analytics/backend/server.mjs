import { createServer } from 'node:http';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import pg from 'pg';

// Session hashes are a local demonstration. Replace this lookup with your
// identity provider's verified server-side session before exposing the app.
export function createApp({ tenants, sessions, host, poolFactory = (config) => new pg.Pool(config) }) {
  if (!/^pg\.[a-z0-9-]+-aws\.motherduck\.com$/.test(host)) throw new Error('Use your organization’s MotherDuck Postgres endpoint');
  const entries = Object.entries(tenants);
  if (entries.length === 0 || entries.length > 16) throw new Error('Configure between 1 and 16 tenants for this example');
  for (const [id, tenant] of entries) {
    if (!/^[a-z][a-z0-9_]{0,39}$/.test(id) || typeof tenant.enabled !== 'boolean' || (tenant.enabled && (typeof tenant.token !== 'string' || !tenant.token))) {
      throw new Error('Every tenant needs a valid ID, explicit enabled flag, and a token when enabled');
    }
  }
  if (Object.entries(sessions).some(([hash, id]) => !/^[a-f0-9]{64}$/.test(hash) || !Object.hasOwn(tenants, id))) throw new Error('Invalid session mapping');
  const pools = new Map();
  const server = createServer(async (request, response) => {
    const reply = (status, value) => {
      response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
      response.end(JSON.stringify(value));
    };
    try {
      const url = new URL(request.url, 'http://localhost');
      if (request.method !== 'GET' || url.pathname !== '/usage') return reply(404, { error: 'Not found' });
      const credential = /^Bearer ([^ ]+)$/.exec(request.headers.authorization ?? '')?.[1];
      if (!credential) return reply(401, { error: 'Authentication required' });
      const hash = createHash('sha256').update(credential).digest('hex');
      const id = Object.hasOwn(sessions, hash) ? sessions[hash] : undefined;
      if (!id || !tenants[id].enabled) return reply(403, { error: 'Access denied' });
      if (url.searchParams.has('tenant') && url.searchParams.get('tenant') !== id) return reply(403, { error: 'Access denied' });
      for (const key of url.searchParams.keys()) {
        if (!['tenant', 'start', 'end'].includes(key) || url.searchParams.getAll(key).length !== 1) return reply(400, { error: 'Invalid query parameters' });
      }
      const start = url.searchParams.get('start');
      const end = url.searchParams.get('end');
      const date = (value) => /^\d{4}-\d{2}-\d{2}$/.test(value ?? '') && Number.isFinite(Date.parse(value)) && new Date(value).toISOString().slice(0, 10) === value;
      if (!date(start) || !date(end) || start > end) return reply(400, { error: 'Use valid start and end dates in YYYY-MM-DD order' });
      if (!pools.has(id)) {
        const pool = poolFactory({
          host, port: 5432, database: 'reporting', user: 'user', password: tenants[id].token,
          ssl: { rejectUnauthorized: true }, max: 2, idleTimeoutMillis: 5000,
          connectionTimeoutMillis: 10000, query_timeout: 15000,
          application_name: 'motherduck-terraform-cfa-example', options: `-c session_name=cfa_${id}`,
        });
        pool.on('error', () => {}); // The next request reports a generic failure without logging credentials.
        pools.set(id, pool);
      }
      const { rows } = await pools.get(id).query(
        'SELECT usage_date::VARCHAR AS usage_date, event_count::VARCHAR AS event_count FROM app.daily_usage WHERE usage_date BETWEEN $1::DATE AND $2::DATE ORDER BY usage_date',
        [start, end],
      );
      reply(200, { tenant: id, rows });
    } catch {
      reply(502, { error: 'Analytics query failed' });
    }
  });
  return {
    server,
    async close() {
      await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
      await Promise.all([...pools.values()].map((pool) => pool.end()));
    },
  };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const tenants = JSON.parse(readFileSync(process.env.TENANT_CREDENTIALS_FILE, 'utf8'));
  const sessions = JSON.parse(readFileSync(process.env.APP_SESSIONS_FILE, 'utf8'));
  const app = createApp({ tenants, sessions, host: process.env.MOTHERDUCK_PG_HOST });
  app.server.listen(Number(process.env.PORT ?? 3000), '127.0.0.1');
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => app.close().then(() => process.exit(0)));
}
