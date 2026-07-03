import type { ReactNode } from 'react'
import { TagChip, TypeMarker, type TaskStatus } from './chips'

export function NoteList({ children }: { children: ReactNode }) {
  return <div className="v-notelist">{children}</div>
}

interface NoteListItemProps {
  title: string
  excerpt?: string
  tags?: string[]
  age?: string
  type?: 'task' | 'knowledge'
  status?: TaskStatus
  selected?: boolean
  /** Active in the workspace: white bg + 3px accent inset stripe. */
  active?: boolean
  onClick?: () => void
}

export function NoteListItem({
  title,
  excerpt,
  tags = [],
  age,
  type = 'knowledge',
  status,
  selected,
  active,
  onClick,
}: NoteListItemProps) {
  const cls = [
    'v-note-item',
    selected && 'v-note-item--selected',
    active && 'v-note-item--active',
  ]
    .filter(Boolean)
    .join(' ')
  return (
    <div className={cls} onClick={onClick}>
      <div className="v-note-item__title-row">
        <TypeMarker type={type} status={status} />
        <span className="v-note-item__title">{title}</span>
      </div>
      {excerpt && <div className="v-note-item__excerpt">{excerpt}</div>}
      <div className="v-note-item__meta">
        {tags.map((t) => (
          <TagChip key={t} tag={t} small />
        ))}
        <span className="v-note-item__spacer" />
        {age && <span className="v-note-item__age">{age}</span>}
      </div>
    </div>
  )
}
