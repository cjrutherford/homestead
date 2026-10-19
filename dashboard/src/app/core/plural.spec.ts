import { plural } from './plural';

describe('plural', () => {
  it('uses the singular only for one', () => {
    expect(plural(1, 'route')).toBe('1 route');
    expect(plural(0, 'route')).toBe('0 routes');
    expect(plural(24, 'hour')).toBe('24 hours');
  });
});
