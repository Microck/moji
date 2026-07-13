'use client';

import { useEffect, useId, useRef } from 'react';

export function Mermaid({ chart }: { chart: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const diagramId = useId().replaceAll(':', '');

  useEffect(() => {
    let cancelled = false;

    async function render() {
      const container = containerRef.current;
      if (!container) return;

      const { default: mermaid } = await import('mermaid');
      const dark = document.documentElement.classList.contains('dark');

      mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'strict',
        theme: dark ? 'dark' : 'default',
      });

      try {
        const { svg } = await mermaid.render(`mermaid-${diagramId}`, chart);
        if (!cancelled) container.innerHTML = svg;
      } catch {
        if (!cancelled) container.textContent = chart;
      }
    }

    void render();

    // Mermaid writes theme colors into its SVG, so it must re-render when the
    // Fumadocs theme class changes instead of relying on inherited CSS colors.
    const observer = new MutationObserver(() => void render());
    observer.observe(document.documentElement, { attributeFilter: ['class'] });

    return () => {
      cancelled = true;
      observer.disconnect();
    };
  }, [chart, diagramId]);

  return <div ref={containerRef} className="my-6 flex justify-center overflow-x-auto" />;
}
