import { isMarkdownPreferred, rewritePath } from 'fumadocs-core/negotiation';
import { NextRequest, NextResponse } from 'next/server';
import { docsContentRoute, docsRoute } from '@/lib/shared';

const { rewrite: rewriteDocs } = rewritePath(`${docsRoute}{/*path}`, `${docsContentRoute}{/*path}/content.md`);
const { rewrite: rewriteSuffix } = rewritePath(`${docsRoute}{/*path}.md`, `${docsContentRoute}{/*path}/content.md`);

export default function proxy(request: NextRequest) {
  const suffixResult = rewriteSuffix(request.nextUrl.pathname);
  if (suffixResult) return NextResponse.rewrite(new URL(suffixResult, request.nextUrl));

  if (isMarkdownPreferred(request)) {
    const docsResult = rewriteDocs(request.nextUrl.pathname);
    if (docsResult) return NextResponse.rewrite(new URL(docsResult, request.nextUrl));
  }

  return NextResponse.next();
}
