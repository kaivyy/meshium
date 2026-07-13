import { describe, it, expect } from 'vitest';
import { redactForDisplay } from '$lib/redact';

describe('redactForDisplay', () => {
  it('masks connection-string credentials, keeps host', () => {
    const out = redactForDisplay('connecting to postgres://admin:s3cr3t@db.example.com:5432/app');
    expect(out).not.toContain('s3cr3t');
    expect(out).toContain('db.example.com');
    expect(out).toContain('••••••••');
  });

  it('masks -pPASSWORD flags but not numeric CLI ports', () => {
    expect(redactForDisplay('redis-cli -pmypassword get x')).not.toContain('mypassword');
    expect(redactForDisplay('redis-cli -p6379 get x')).toContain('-p6379');
  });

  it('masks REDISCLI_AUTH / PGPASSWORD env assignments', () => {
    expect(redactForDisplay('REDISCLI_AUTH=topsecret redis-cli')).toContain('REDISCLI_AUTH=••••••');
    expect(redactForDisplay('PGPASSWORD=hunter2 ./migrate')).toContain('PGPASSWORD=••••••');
  });

  it('masks Bearer tokens and Authorization headers', () => {
    expect(redactForDisplay('Authorization: Bearer abc123xyz')).toContain('••••••');
    expect(redactForDisplay('header: Bearer tok-tok-tok more')).toContain('Bearer ••••••');
  });

  it('masks password/secret JSON keys but leaves other keys intact', () => {
    const out = redactForDisplay('{"username":"bob","password":"hunter2","role":"admin"}');
    expect(out).not.toContain('hunter2');
    expect(out).toContain('"username":"bob"');
    expect(out).toContain('"role":"admin"');
  });

  it('handles null/undefined/empty safely', () => {
    expect(redactForDisplay(null)).toBe('');
    expect(redactForDisplay(undefined)).toBe('');
    expect(redactForDisplay('')).toBe('');
  });

  it('does not alter innocuous log lines', () => {
    const line = 'replication lag 2s, health 95, step=live_replication';
    expect(redactForDisplay(line)).toBe(line);
  });
});
