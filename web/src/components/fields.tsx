import type { InputHTMLAttributes, ReactNode, TextareaHTMLAttributes } from 'react'

export function Label({ children }: { children: ReactNode }) {
  return <label className="v-label">{children}</label>
}

export function TextField(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input className="v-input" {...props} />
}

interface SearchInputProps extends InputHTMLAttributes<HTMLInputElement> {
  shortcut?: string
}

export function SearchInput({ shortcut = '⌘K', ...rest }: SearchInputProps) {
  return (
    <div className="v-search">
      <span className="v-search__icon">⌕</span>
      <input className="v-search__input" placeholder="Search notes…" {...rest} />
      {shortcut && <span className="v-kbd">{shortcut}</span>}
    </div>
  )
}

export function Textarea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className="v-textarea" {...props} />
}

interface SelectProps {
  children: ReactNode
  onClick?: () => void
}

/** Custom select trigger per design (▾ caret, optional status dot inside). */
export function SelectTrigger({ children, onClick }: SelectProps) {
  return (
    <div className="v-select" onClick={onClick} role="button" tabIndex={0}>
      <span className="v-select__value">{children}</span>
      <span className="v-select__caret">▾</span>
    </div>
  )
}

export function Kbd({ children }: { children: ReactNode }) {
  return <span className="v-kbd">{children}</span>
}
