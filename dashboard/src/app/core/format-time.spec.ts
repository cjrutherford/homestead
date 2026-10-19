import { formatWhen, sentence } from './format-time';

describe('formatWhen', () => {
  it('formats a nanosecond timestamp in UTC without the fraction', () => {
    expect(formatWhen('2026-10-19T19:18:00.434603569Z')).toBe('19 Oct 2026, 19:18 UTC');
  });
  it('uses the fallback for empty and zero times', () => {
    expect(formatWhen('', 'never')).toBe('never');
    expect(formatWhen(null)).toBe('');
    expect(formatWhen('0001-01-01T00:00:00Z', 'never')).toBe('never');
  });
  it('returns text that is not a date unchanged', () => {
    expect(formatWhen('soon')).toBe('soon');
  });
});

describe('sentence', () => {
  it('capitalises the first letter', () => {
    expect(sentence('no recorded issuances: none')).toBe('No recorded issuances: none');
    expect(sentence('')).toBe('');
  });
});
