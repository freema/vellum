import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

/**
 * Rendered note body, shared by the workspace preview and the public share
 * page. Both behaviours that make it interactive are opt-in props, because
 * the share page must have neither: a reader has no vault to navigate into
 * and no permission to edit.
 *
 * - `onWikilink` absent → [[links]] render as plain text, not anchors.
 * - `onToggleCheckbox` absent → task boxes render, but do not respond.
 */
export function MarkdownView({
  body,
  onWikilink,
  onToggleCheckbox,
}: {
  body: string
  onWikilink?: (target: string) => void
  onToggleCheckbox?: (index: number) => void
}) {
  // Wikilinks are not markdown, so they are rewritten to anchors with a
  // private scheme and picked back out in the `a` renderer below.
  const processed = body.replace(
    /\[\[([^\][|]+)(?:\|([^\][]+))?\]\]/g,
    (_, target: string, alias?: string) =>
      `[${alias ?? target}](#wikilink=${encodeURIComponent(target.trim())})`,
  )
  const cb = { i: 0 }
  return (
    <div className="v-markdown">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          a({ href, children }) {
            if (href?.startsWith('#wikilink=')) {
              const target = decodeURIComponent(href.slice('#wikilink='.length))
              if (!onWikilink) {
                return <span className="v-wikilink v-wikilink--flat">[[{children}]]</span>
              }
              return (
                <a
                  className="v-wikilink"
                  onClick={(e) => {
                    e.preventDefault()
                    onWikilink(target)
                  }}
                >
                  [[{children}]]
                </a>
              )
            }
            return (
              <a href={href} target="_blank" rel="noreferrer">
                {children}
              </a>
            )
          },
          input({ checked }) {
            const idx = cb.i++
            return (
              <span
                className={`v-checkbox${checked ? ' v-checkbox--checked' : ''}${onToggleCheckbox ? ' v-checkbox--live' : ''}`}
                onClick={onToggleCheckbox ? () => onToggleCheckbox(idx) : undefined}
              >
                {checked ? '✓' : ''}
              </span>
            )
          },
        }}
      >
        {processed}
      </ReactMarkdown>
    </div>
  )
}
