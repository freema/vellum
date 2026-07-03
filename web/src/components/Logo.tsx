import type { CSSProperties } from 'react'

type MarkVariant = 'paper' | 'panel' | 'midnight' | 'reversed'

const variants: Record<
  MarkVariant,
  { tile: string; notch: string; fold: string; letter: string }
> = {
  // notch always matches the surface behind the tile
  paper: { tile: '#8B6F47', notch: '#FAF7F2', fold: '#6F5836', letter: '#FBF7F0' },
  panel: { tile: '#8B6F47', notch: '#FFFFFF', fold: '#6F5836', letter: '#FBF7F0' },
  midnight: { tile: '#C2A878', notch: '#16141F', fold: '#9E8351', letter: '#16141F' },
  reversed: { tile: '#FBF7F0', notch: '#8B6F47', fold: '#D8CBB4', letter: '#8B6F47' },
}

/**
 * The vellum mark: a rounded parchment tile with a turned top-right corner
 * and a centered serif "v". Construction from design/Vellum-Logo.dc.html —
 * radius ≈ size/6.4, fold ≈ size/3, letter ≈ 0.57×size nudged by ~size/9.
 */
export function LogoMark({
  size = 40,
  variant = 'paper',
  surface,
}: {
  size?: number
  variant?: MarkVariant
  /** Override the notch color to match a custom surface. */
  surface?: string
}) {
  const c = variants[variant]
  const fold = Math.round(size / 3 * 1.05)
  const radius = Math.round(size / 6.4)
  const notchRadius = Math.max(2, Math.round(fold / 2.7))
  const letterSize = Math.round(size * 0.575)
  const pad = Math.round(size / 9)
  const box: CSSProperties = { position: 'relative', width: size, height: size, flex: 'none' }
  return (
    <div style={box} aria-label="vellum">
      <div style={{ position: 'absolute', inset: 0, background: c.tile, borderRadius: radius }} />
      <div
        style={{
          position: 'absolute',
          top: 0,
          right: 0,
          width: fold,
          height: fold,
          background: surface ?? c.notch,
          borderBottomLeftRadius: notchRadius,
        }}
      />
      {size > 20 && (
        <div
          style={{
            position: 'absolute',
            top: 0,
            right: 0,
            width: fold,
            height: fold,
            background: c.fold,
            clipPath: 'polygon(0 0, 100% 100%, 0 100%)',
          }}
        />
      )}
      <div
        style={{
          position: 'absolute',
          inset: 0,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          paddingTop: size > 20 ? pad : 0,
        }}
      >
        <span
          style={{
            fontFamily: 'var(--font-serif)',
            fontSize: letterSize,
            fontWeight: 500,
            color: c.letter,
            lineHeight: 1,
          }}
        >
          v
        </span>
      </div>
    </div>
  )
}

export function Wordmark({ size = 32 }: { size?: number }) {
  return (
    <span
      style={{
        fontFamily: 'var(--font-serif)',
        fontSize: size,
        fontWeight: 500,
        letterSpacing: '-0.01em',
        lineHeight: 1,
        color: 'var(--ink)',
      }}
    >
      vellum
    </span>
  )
}

/** Horizontal lockup: 44px mark + 36px wordmark (per design). */
export function LogoLockup() {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
      <LogoMark size={44} variant="panel" />
      <Wordmark size={36} />
    </div>
  )
}
