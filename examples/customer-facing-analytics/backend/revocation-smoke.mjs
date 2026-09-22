import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';
import pg from 'pg';
const outputs = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const token = outputs.reader_tokens.value.acme;
const host = process.env.MOTHERDUCK_PG_HOST ?? 'pg.us-east-1-aws.motherduck.com';
let denied = false;
for (let attempt = 0; attempt < 30; attempt++) {
  const client = new pg.Client({ host, port: 5432, database: 'reporting', user: 'user', password: token, ssl: { rejectUnauthorized: true }, connectionTimeoutMillis: 5000, query_timeout: 5000 });
  try {
    await client.connect();
    await client.query('SELECT count(*) FROM app.daily_usage');
  } catch (error) {
    if (/auth|token|permission|denied|revok|expired/i.test(error.message)) { denied = true; break; }
    throw error;
  } finally { await client.end(); }
  await new Promise((resolve) => setTimeout(resolve, 2000));
}
assert.equal(denied, true, 'Suspended reader credential still permits fresh authenticated queries');
console.log('PASS: suspended tenant credential rejected on a fresh connection');
