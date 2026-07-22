import type { Metadata } from 'next';
import { Google_Sans_Flex } from 'next/font/google';
import { RootProvider } from 'fumadocs-ui/provider/next';
import './global.css';

const siteUrl = process.env.NEXT_PUBLIC_SITE_URL ?? 'https://moji.micr.dev';
const description = 'Find and safely download fonts from the terminal.';
const structuredData = {
  '@context': 'https://schema.org',
  '@type': 'SoftwareApplication',
  name: 'Moji',
  url: siteUrl,
  description,
  applicationCategory: 'DeveloperApplication',
  operatingSystem: 'Linux, macOS, Windows',
  isAccessibleForFree: true,
  downloadUrl: 'https://github.com/Microck/moji/releases',
  sameAs: ['https://github.com/Microck/moji'],
};

const googleSansFlex = Google_Sans_Flex({
  // Next does not ship fallback metrics for Google Sans Flex yet.
  adjustFontFallback: false,
  axes: ['opsz'],
  display: 'swap',
  subsets: ['latin'],
  variable: '--font-google-sans-flex',
  weight: 'variable',
});

export const metadata: Metadata = {
  metadataBase: new URL(siteUrl),
  title: { default: 'Moji', template: '%s | Moji' },
  description,
  applicationName: 'Moji',
  alternates: { canonical: '/' },
  openGraph: {
    type: 'website',
    url: '/',
    siteName: 'Moji',
    title: 'Moji',
    description,
  },
  twitter: {
    card: 'summary',
    title: 'Moji',
    description,
    creator: '@JustMicrock',
  },
  robots: { index: true, follow: true },
};

export default function Layout({ children }: LayoutProps<'/'>) {
  return (
    <html className={googleSansFlex.variable} lang="en" suppressHydrationWarning>
      <head>
        <script
          dangerouslySetInnerHTML={{ __html: JSON.stringify(structuredData) }}
          type="application/ld+json"
        />
      </head>
      <body className="flex min-h-screen flex-col font-sans antialiased">
        <RootProvider>{children}</RootProvider>
      </body>
    </html>
  );
}
