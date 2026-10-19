/** "1 route", "2 routes": a count with its noun, so copy never says "route(s)". */
export function plural(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? '' : 's'}`;
}
