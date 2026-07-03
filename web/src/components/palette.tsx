import type { ReactNode } from 'react'

export function CommandPalette({
  query,
  onQueryChange,
  children,
}: {
  query: string
  onQueryChange?: (q: string) => void
  children: ReactNode
}) {
  return (
    <div className="v-palette">
      <div className="v-palette__header">
        <span className="v-palette__icon">⌕</span>
        <input
          className="v-palette__query"
          value={query}
          onChange={(e) => onQueryChange?.(e.target.value)}
          autoFocus
        />
        <span className="v-palette__esc">esc</span>
      </div>
      <div className="v-palette__results">{children}</div>
      <div className="v-palette__footer">
        <span>↑↓ navigate</span>
        <span>↵ open</span>
        <span>#tag filter</span>
      </div>
    </div>
  )
}

export function PaletteItem({
  title,
  path,
  snippet,
  selected,
  onClick,
}: {
  title: string
  path: string
  snippet?: ReactNode
  selected?: boolean
  onClick?: () => void
}) {
  return (
    <div
      className={`v-palette__item${selected ? ' v-palette__item--selected' : ''}`}
      onClick={onClick}
    >
      <div className="v-palette__item-title-row">
        <span className="v-palette__item-title">{title}</span>
        <span className="v-palette__item-path">{path}</span>
      </div>
      {snippet && <div className="v-palette__item-snippet">{snippet}</div>}
    </div>
  )
}

/** Search match highlight, e.g. <>…<Highlight>roadmap</Highlight>…</> */
export function Highlight({ children }: { children: ReactNode }) {
  return <span className="v-highlight">{children}</span>
}
