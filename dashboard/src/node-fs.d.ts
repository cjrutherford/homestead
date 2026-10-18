/** Minimal typing for specs that read repository files (no @types/node). */
declare module 'node:fs' {
  export function readFileSync(path: string, encoding: 'utf8'): string;
}
