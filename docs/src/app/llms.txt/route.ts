import { source } from '@/lib/source';

export const revalidate = false;

const SITE_URL = process.env.NEXT_PUBLIC_SITE_URL ?? 'https://moji.micr.dev';

/* eslint-disable @typescript-eslint/no-explicit-any */
type TreeNode = any;

/** Escape markdown link syntax in titles. */
function escapeTitle(title: string): string {
  return title.replace(/([[\]])/g, '\\$1');
}

/** Escape parentheses in URLs. */
function escapeUrl(url: string): string {
  return url.replace(/([()])/g, '\\$1');
}

/** Format a single page as a list item with absolute URL. */
function formatPage(name: string, url: string, description: string, indent: number): string {
  const prefix = '  '.repeat(indent);
  const link = `[${escapeTitle(name)}](${escapeUrl(url)})`;
  const desc = description.trim();
  return desc.length > 0 ? `${prefix}- ${link}: ${desc}` : `${prefix}- ${link}`;
}

/** Extract a plain-text name from a React node (separators may use React). */
function nodeToText(name: unknown): string {
  if (typeof name === 'string') return name;
  if (name == null) return '';
  return String(name);
}

/** Recursively format the page tree node into llms.txt lines. */
function formatNode(node: TreeNode, indent: number, lines: string[]): void {
  const type: string = node?.type;
  if (!type) return;

  if (type === 'page') {
    const url: string = node.url;
    const title = nodeToText(node.name);
    const desc = nodeToText(node.description);
    lines.push(formatPage(title, `${SITE_URL}${url}`, desc, indent));
    return;
  }

  if (type === 'separator') {
    const name = nodeToText(node.name) || 'Separator';
    lines.push('');
    lines.push(`## ${name}`);
    return;
  }

  if (type === 'folder') {
    if (node.index) {
      formatNode(node.index as TreeNode, indent, lines);
    }
    const children: TreeNode[] = (node.children ?? []) as TreeNode[];
    for (const child of children) {
      formatNode(child, indent + 1, lines);
    }
  }
}

export function GET() {
  const pageTree = source.getPageTree();
  const lines: string[] = [];

  lines.push(`# ${nodeToText(pageTree.name) || 'Moji'}`);
  lines.push('');
  lines.push('> Find, inspect, and safely download fonts from the terminal.');

  for (const child of pageTree.children) {
    formatNode(child, 0, lines);
  }

  return new Response(lines.join('\n'), {
    headers: {
      'Content-Type': 'text/plain; charset=utf-8',
      'Cache-Control': 'public, max-age=3600',
    },
  });
}
