import { createMDX } from 'fumadocs-mdx/next';
import { resolve } from 'node:path';

const withMDX = createMDX();

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  // Keep the offline regression build separate from production/browser artifacts.
  distDir: process.env.MOJI_FONT_REGRESSION === '1' ? '.next-font-regression' : '.next',
  ...(process.env.MOJI_FONT_REGRESSION === '1'
    ? { typescript: { tsconfigPath: 'tsconfig.font-regression.json' } }
    : {}),
  turbopack: {
    root: resolve(import.meta.dirname, '..'),
  },
};

export default withMDX(config);
