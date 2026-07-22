import type { MetadataRoute } from 'next';
import { source } from '@/lib/source';

const siteUrl = process.env.NEXT_PUBLIC_SITE_URL ?? 'https://moji.micr.dev';

export default function sitemap(): MetadataRoute.Sitemap {
  return [
    { url: new URL('/', siteUrl).toString() },
    ...source.getPages().map((page) => ({
      url: new URL(page.url, siteUrl).toString(),
    })),
  ];
}
