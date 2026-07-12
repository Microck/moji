import type { Metadata } from 'next';
import { Google_Sans_Flex } from 'next/font/google';
import { RootProvider } from 'fumadocs-ui/provider/next';
import './global.css';

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
  metadataBase: new URL(process.env.NEXT_PUBLIC_SITE_URL ?? 'http://localhost:3000'),
  title: { default: 'Moji', template: '%s | Moji' },
  description: 'Find and safely download fonts from the terminal.',
};

export default function Layout({ children }: LayoutProps<'/'>) {
  return (
    <html className={googleSansFlex.variable} lang="en" suppressHydrationWarning>
      <body className="flex min-h-screen flex-col font-sans antialiased">
        <RootProvider>{children}</RootProvider>
      </body>
    </html>
  );
}
