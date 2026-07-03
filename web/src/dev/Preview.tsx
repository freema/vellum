// Dev-only component preview (no Storybook by design) — used for the
// side-by-side visual check against design/Vellum-Design-System.dc.html.
import { useState } from 'react'
import { Button } from '../components/Button'
import { Label, TextField, SearchInput, Textarea, SelectTrigger, Kbd } from '../components/fields'
import { TagChip, StatusBadge, StatusDot, TypeMarker } from '../components/chips'
import { Tree, TreeItem, TreeChildren } from '../components/tree'
import { NoteList, NoteListItem } from '../components/notes'
import { CommandPalette, PaletteItem, Highlight } from '../components/palette'
import { Toast, Breadcrumb, StatusBar, ConfirmModal, EmptyState } from '../components/feedback'
import { LogoMark, Wordmark, LogoLockup } from '../components/Logo'

function Section({ n, title, children }: { n: string; title: string; children: React.ReactNode }) {
  return (
    <section style={{ marginBottom: 88 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 14, marginBottom: 28 }}>
        <span style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--accent)' }}>{n}</span>
        <h2 style={{ fontFamily: 'var(--font-serif)', fontWeight: 500, fontSize: 26, margin: 0 }}>{title}</h2>
      </div>
      {children}
    </section>
  )
}

const panel: React.CSSProperties = {
  background: 'var(--bg-panel)',
  border: '1px solid var(--line)',
  borderRadius: 8,
  padding: 32,
}

export default function Preview() {
  const [dark, setDark] = useState(false)
  const toggle = () => {
    const next = !dark
    setDark(next)
    document.documentElement.setAttribute('data-theme', next ? 'dark' : 'light')
  }

  return (
    <div style={{ background: 'var(--canvas)', minHeight: '100vh', padding: '56px 0 96px', color: 'var(--ink)' }}>
      <div style={{ maxWidth: 1040, margin: '0 auto', padding: '0 40px' }}>
        <header style={{ marginBottom: 64, display: 'flex', alignItems: 'center', gap: 24 }}>
          <LogoLockup />
          <span style={{ flex: 1 }} />
          <Button variant="secondary" onClick={toggle}>
            {dark ? 'paper' : 'midnight ink'}
          </Button>
        </header>

        <Section n="03" title="Buttons">
          <div style={{ ...panel, display: 'flex', alignItems: 'center', gap: 14, flexWrap: 'wrap' }}>
            <Button variant="primary">Save note</Button>
            <Button variant="secondary">Move to…</Button>
            <Button variant="ghost">Cancel</Button>
            <Button variant="danger">Delete</Button>
            <span style={{ width: 1, height: 32, background: 'var(--line)', margin: '0 6px' }} />
            <Button variant="icon">＋</Button>
            <Button variant="icon">⋯</Button>
          </div>
        </Section>

        <Section n="04" title="Inputs">
          <div style={{ ...panel, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 24 }}>
            <div>
              <Label>Note title</Label>
              <TextField defaultValue="roadmap" />
            </div>
            <div>
              <Label>Search</Label>
              <SearchInput />
            </div>
            <div style={{ gridColumn: '1 / -1' }}>
              <Label>Raw markdown</Label>
              <Textarea defaultValue={'## Notes\n- self-hosted, single binary\n- MCP over http'} style={{ height: 80 }} />
            </div>
            <div>
              <Label>Folder</Label>
              <SelectTrigger>projects / vellum</SelectTrigger>
            </div>
            <div>
              <Label>Status</Label>
              <SelectTrigger>
                <StatusDot status="in-progress" /> In progress
              </SelectTrigger>
            </div>
          </div>
        </Section>

        <Section n="05" title="Tags & status">
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 24 }}>
            <div style={{ ...panel, padding: 28 }}>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                <TagChip tag="go" />
                <TagChip tag="mcp" />
                <TagChip tag="roadmap" onRemove={() => {}} />
              </div>
              <div style={{ display: 'flex', gap: 32, marginTop: 24 }}>
                <span style={{ display: 'flex', alignItems: 'center', gap: 11 }}>
                  <TypeMarker type="task" status="in-progress" /> task
                </span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 11 }}>
                  <TypeMarker type="knowledge" /> knowledge
                </span>
              </div>
            </div>
            <div style={{ ...panel, padding: 28, display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'flex-start' }}>
              <StatusBadge status="backlog" />
              <StatusBadge status="in-progress" />
              <StatusBadge status="done" />
            </div>
          </div>
        </Section>

        <Section n="06" title="Tree & note list">
          <div style={{ display: 'grid', gridTemplateColumns: '240px 1fr', gap: 24 }}>
            <Tree>
              <TreeItem label="Inbox" count={12} badge hasChildren expanded={true} muted />
              <TreeItem label="Projects" count={28} hasChildren expanded selected />
              <TreeChildren>
                <TreeItem label="vellum" count={9} child activeChild />
                <TreeItem label="physiohub" count={14} child />
              </TreeChildren>
              <TreeItem label="Archive" count={63} hasChildren expanded={false} muted />
            </Tree>
            <NoteList>
              <NoteListItem
                title="Roadmap"
                type="task"
                status="in-progress"
                excerpt="Vault CRUD, search, tags — then the MCP layer and OAuth."
                tags={['go', 'mcp']}
                age="2d"
              />
              <NoteListItem
                title="Embedded SPA build"
                type="task"
                status="done"
                excerpt="Vite build embedded via go:embed, one binary ships everything."
                tags={['build']}
                age="5d"
                selected
              />
              <NoteListItem
                title="Frontmatter schema"
                type="task"
                status="backlog"
                excerpt="type: task|knowledge, status, tags — nothing else in v1."
                tags={['spec']}
                age="1w"
              />
            </NoteList>
          </div>
        </Section>

        <Section n="07" title="Command palette">
          <div style={{ background: 'var(--canvas)', border: '1px solid var(--line)', borderRadius: 10, padding: 44, display: 'flex', justifyContent: 'center' }}>
            <CommandPalette query="roadmap">
              <PaletteItem
                title="Roadmap"
                path="projects/vellum"
                snippet={<>Vault CRUD, search, tags — the <Highlight>roadmap</Highlight> for v1.</>}
                selected
              />
              <PaletteItem
                title="Old roadmap ideas"
                path="archive"
                snippet={<>Discarded <Highlight>roadmap</Highlight> drafts from the first sketch.</>}
              />
            </CommandPalette>
          </div>
        </Section>

        <Section n="08" title="Markdown preview">
          <div className="v-markdown" style={{ ...panel, padding: '40px 44px', maxWidth: 720 }}>
            <h1>Vellum roadmap</h1>
            <p>
              A calm window into a folder of markdown. See <a href="#top">the brief</a> for
              positioning; this note tracks delivery.
            </p>
            <h2>Milestones</h2>
            <div className="v-task-list">
              <li>
                <span className="v-checkbox v-checkbox--checked">✓</span> Bootstrap the repo
              </li>
              <li>
                <span className="v-checkbox" /> Ship the embedded SPA
              </li>
            </div>
            <blockquote>Structure is a feature — inbox, projects, archive.</blockquote>
            <p>
              Linked spec: <span className="v-wikilink">[[frontmatter schema]]</span> covers the
              task states.
            </p>
            <pre>{`type: task\nstatus: in-progress\ntags: [go, mcp]`}</pre>
          </div>
        </Section>

        <Section n="09" title="Feedback">
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 24, alignItems: 'start' }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <Toast kbd="⌘S">Note saved</Toast>
              <Toast kind="error">Couldn't write to vault — check permissions</Toast>
              <div style={{ ...panel, padding: '14px 16px' }}>
                <Breadcrumb segments={['projects', 'vellum']} current="roadmap.md" />
              </div>
              <StatusBar path="projects/vellum/roadmap.md" noteCount={128} saved />
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
              <ConfirmModal title="Delete this note?" confirmLabel="Delete">
                This permanently removes <span className="v-modal__code">roadmap.md</span> from the
                vault. There is no undo.
              </ConfirmModal>
              <EmptyState title="Inbox zero">Nothing to process. New captures land here.</EmptyState>
            </div>
          </div>
        </Section>

        <Section n="10" title="Logo">
          <div style={{ ...panel, display: 'flex', alignItems: 'center', gap: 40, flexWrap: 'wrap' }}>
            <LogoMark size={132} variant="panel" />
            <div>
              <Wordmark size={84} />
              <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12.5, color: 'var(--accent)', marginTop: 18, letterSpacing: '0.02em' }}>
                a calm window into a folder of markdown
              </div>
            </div>
            <span style={{ flex: 1 }} />
            {(
              [
                ['on paper', '#FAF7F2', '#E8E2D8', 'paper'],
                ['on panel', '#FFFFFF', '#E8E2D8', 'panel'],
                ['midnight', '#16141F', '#2E2A3A', 'midnight'],
                ['reversed', '#8B6F47', '#7A6039', 'reversed'],
              ] as const
            ).map(([label, bg, border, variant]) => (
              <div
                key={label}
                style={{
                  background: bg,
                  border: `1px solid ${border}`,
                  borderRadius: 10,
                  padding: 28,
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  gap: 14,
                }}
              >
                <LogoMark size={64} variant={variant} />
                <span style={{ fontFamily: 'var(--font-mono)', fontSize: 10, color: variant === 'midnight' ? '#8F8A9B' : variant === 'reversed' ? '#EBDFC9' : '#9A938A' }}>
                  {label}
                </span>
              </div>
            ))}
            <LogoMark size={32} variant="panel" />
            <LogoMark size={16} variant="panel" />
          </div>
        </Section>

        <footer style={{ borderTop: '1px solid var(--line)', paddingTop: 20, fontFamily: 'var(--font-mono)', fontSize: 11, color: 'var(--ink-faint)', display: 'flex', justifyContent: 'space-between' }}>
          <span>vellum design system</span>
          <span>paper · ink · quiet</span>
        </footer>
      </div>
      <div style={{ position: 'fixed', bottom: 16, right: 16 }}>
        <Kbd>dev preview</Kbd>
      </div>
    </div>
  )
}
