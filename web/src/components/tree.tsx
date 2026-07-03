import type { ReactNode } from 'react'

interface TreeItemProps {
  label: string
  count?: number | string
  /** Filled accent pill badge (e.g. inbox unprocessed count). */
  badge?: boolean
  expanded?: boolean
  /** Leaf/collapsed folders show ▸, expanded show ▾; undefined hides caret. */
  hasChildren?: boolean
  selected?: boolean
  activeChild?: boolean
  child?: boolean
  muted?: boolean
  onClick?: () => void
}

export function TreeItem({
  label,
  count,
  badge,
  expanded,
  hasChildren,
  selected,
  activeChild,
  child,
  muted,
  onClick,
}: TreeItemProps) {
  const cls = [
    'v-tree__item',
    child && 'v-tree__item--child',
    muted && 'v-tree__item--muted',
    selected && 'v-tree__item--selected',
    activeChild && 'v-tree__item--active-child',
  ]
    .filter(Boolean)
    .join(' ')
  const strong = selected || activeChild
  return (
    <div className={cls} onClick={onClick}>
      {hasChildren !== undefined && (
        <span className="v-tree__caret">{expanded ? '▾' : '▸'}</span>
      )}
      <span className="v-tree__folder" style={strong ? { color: 'var(--accent)' } : undefined}>
        {selected || activeChild ? '▤' : '▢'}
      </span>
      <span className={`v-tree__label${strong ? ' v-tree__label--strong' : ''}`}>{label}</span>
      {badge && count !== undefined ? (
        <span className="v-tree__badge">{count}</span>
      ) : count !== undefined ? (
        <span className={`v-tree__count${selected ? ' v-tree__count--muted' : ''}`}>{count}</span>
      ) : null}
    </div>
  )
}

export function Tree({ children }: { children: ReactNode }) {
  return <div className="v-tree">{children}</div>
}

export function TreeChildren({ children }: { children: ReactNode }) {
  return <div className="v-tree__children">{children}</div>
}
