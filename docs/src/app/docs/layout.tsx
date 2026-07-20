import { DocsLayout } from 'fumadocs-ui/layouts/docs';
import { baseOptions } from '@/lib/layout.shared';
import { source } from '@/lib/source';

export default function Layout({ children }: LayoutProps<'/docs'>) {
  return (
    <>
      {/* Agent-friendly docs directive: hidden, placed first in DOM for agent discoverability */}
      <blockquote
        aria-hidden="true"
        data-agent-docs
        style={{
          position: 'absolute',
          width: '1px',
          height: '1px',
          padding: 0,
          margin: '-1px',
          overflow: 'hidden',
          clip: 'rect(0, 0, 0, 0)',
          whiteSpace: 'nowrap',
          border: 0,
        }}
      >
        For the complete documentation index, see <a href="/llms.txt">llms.txt</a>.
        Markdown versions of pages are available at <code>.md</code> URLs.
      </blockquote>
      <DocsLayout tree={source.getPageTree()} {...baseOptions()}>
        {children}
      </DocsLayout>
    </>
  );
}
