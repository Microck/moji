import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';
import { appName } from './shared';

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <span className="inline-flex items-center gap-2">
          <span aria-label="Mona, Moji's mascot" className="font-mono text-orange-500">（ ´∀｀）</span>
          {appName}
        </span>
      ),
    },
  };
}
