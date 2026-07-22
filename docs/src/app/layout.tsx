import type { Metadata } from 'next';
import { Google_Sans_Flex } from 'next/font/google';
import { RootProvider } from 'fumadocs-ui/provider/next';
import './global.css';

const siteUrl = process.env.NEXT_PUBLIC_SITE_URL ?? 'https://moji.micr.dev';
const title = 'Moji - Font Downloader for the Terminal';
const description = 'Find and safely download fonts from the terminal.';
const structuredData = {
  '@context': 'https://schema.org',
  '@type': 'SoftwareSourceCode',
  name: 'Moji',
  url: siteUrl,
  description,
  codeRepository: 'https://github.com/Microck/moji',
  programmingLanguage: 'Rust',
  runtimePlatform: 'Linux, macOS, Windows',
  isAccessibleForFree: true,
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
  title: { default: title, template: '%s | Moji' },
  description,
  applicationName: 'Moji',
  alternates: { canonical: new URL('/', siteUrl).toString() },
  openGraph: {
    type: 'website',
    url: '/',
    siteName: 'Moji',
    title,
    description,
  },
  twitter: {
    card: 'summary',
    title,
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
