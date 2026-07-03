export type TaskStatus = 'backlog' | 'in-progress' | 'done'

const statusLabels: Record<TaskStatus, string> = {
  backlog: 'Backlog',
  'in-progress': 'In progress',
  done: 'Done',
}

export function TagChip({
  tag,
  small,
  onRemove,
  onClick,
}: {
  tag: string
  small?: boolean
  onRemove?: () => void
  onClick?: () => void
}) {
  const cls = ['v-tag', small && 'v-tag--small', onRemove && 'v-tag--removable']
    .filter(Boolean)
    .join(' ')
  return (
    <span className={cls} onClick={onClick} style={onClick ? { cursor: 'pointer' } : undefined}>
      #{tag}
      {onRemove && (
        <span
          className="v-tag__remove"
          onClick={(e) => {
            e.stopPropagation()
            onRemove()
          }}
        >
          ×
        </span>
      )}
    </span>
  )
}

export function StatusDot({ status }: { status: TaskStatus }) {
  return <span className={`v-status-dot v-status-dot--${status}`} />
}

export function StatusBadge({ status }: { status: TaskStatus }) {
  return (
    <span className={`v-status-badge v-status-badge--${status}`}>
      <StatusDot status={status} />
      {statusLabels[status]}
    </span>
  )
}

/** Note type marker: task = round dot, knowledge = rounded square. */
export function TypeMarker({ type, status }: { type: 'task' | 'knowledge'; status?: TaskStatus }) {
  if (type === 'knowledge') {
    return <span className="v-type-marker--knowledge" />
  }
  return <span className={`v-status-dot v-status-dot--${status ?? 'backlog'}`} style={{ width: 9, height: 9 }} />
}
