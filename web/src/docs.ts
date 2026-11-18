// The operator docs are a site (ADR-0071): its root follows the latest
// release, so a build of the control room links there, never to main's
// files or to /next/, which can be ahead of the hub it ships in.
export const DOCS_SITE = 'https://enes-alatas.github.io/spool/'

// A page on the docs site, by its path under the root: `guides/langfuse/`.
export function docsPage(path: string): string {
  return DOCS_SITE + path
}
