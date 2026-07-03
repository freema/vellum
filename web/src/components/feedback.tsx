import type { ReactNode } from 'react'
import { StatusDot } from './chips'

export function Toast({
  kind = 'success',
  children,
  kbd,
}: {
  kind?: 'success' | 'error'
  children: ReactNode
  kbd?: string
}) {
  return (
    <div className={`v-toast${kind === 'error' ? ' v-toast--error' : ''}`}>
      <span
        className="v-status-dot"
        style={{ background: kind === 'error' ? 'var(--danger)' : 'var(--status-done)' }}
      />
      {children}
      <span className="v-toast__spacer" />
      {kbd && <span className="v-toast__kbd">{kbd}</span>}
    </div>
  )
}

export function Breadcrumb({ segments, current }: { segments: string[]; current: string }) {
  return (
    <div className="v-breadcrumb">
      {segments.map((s) => (
        <span key={s}>
          <span className="v-breadcrumb__segment">{s}</span>
          {' / '}
        </span>
      ))}
      <span className="v-breadcrumb__current">{current}</span>
    </div>
  )
}

export function StatusBar({
  path,
  noteCount,
  saved,
}: {
  path: string
  noteCount: number
  saved?: boolean
}) {
  return (
    <div className="v-statusbar">
      <span>{path}</span>
      <span className="v-statusbar__spacer" />
      <span>{noteCount} notes</span>
      {saved && <span className="v-statusbar__saved">Saved ✓</span>}
    </div>
  )
}

export function ConfirmModal({
  title,
  children,
  confirmLabel,
  onCancel,
  onConfirm,
}: {
  title: string
  children: ReactNode
  confirmLabel: string
  onCancel?: () => void
  onConfirm?: () => void
}) {
  return (
    <div className="v-modal">
      <div className="v-modal__title">{title}</div>
      <div className="v-modal__body">{children}</div>
      <div className="v-modal__actions">
        <button className="v-modal__cancel" onClick={onCancel}>
          Cancel
        </button>
        <button className="v-modal__confirm" onClick={onConfirm}>
          {confirmLabel}
        </button>
      </div>
    </div>
  )
}

export function EmptyState({
  glyph = '∅',
  title,
  children,
}: {
  glyph?: string
  title: string
  children?: ReactNode
}) {
  return (
    <div className="v-empty">
      <div className="v-empty__glyph">{glyph}</div>
      <div className="v-empty__title">{title}</div>
      {children && <div className="v-empty__body">{children}</div>}
    </div>
  )
}

export { StatusDot }
