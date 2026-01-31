# TaskYou Demo Video - Design Guide

**Style**: Stripe Midnight
**Inspiration**: Stripe, Vercel
**Vibe**: Deep navy meets electric purple. Trust + innovation. Premium fintech energy for dev tools. Clean, confident, trustworthy.

---

## Color Palette

### Primary Colors

| Name | Hex | Usage |
|------|-----|-------|
| Midnight Navy | `#0a2540` | Primary dark background, hero sections |
| Electric Purple | `#635bff` | Primary accent, CTAs, highlights |
| Cyan Glow | `#80e9ff` | Secondary accent, terminal text, links |
| Cloud White | `#f6f9fc` | Light backgrounds, contrast sections |
| Violet | `#a855f7` | Gradient endpoint, decorative accents |

### Supporting Colors

| Name | Hex | Usage |
|------|-----|-------|
| Pure White | `#ffffff` | Text on dark backgrounds |
| Slate | `#94a3b8` | Secondary text, subtitles |
| Deep Navy | `#1a365d` | Gradient midpoint |

---

## Gradients

### Hero Gradient (Dark)
```css
background: linear-gradient(135deg, #0a2540 0%, #1a365d 100%);
```

### Accent Gradient (Purple)
```css
background: linear-gradient(135deg, #635bff 0%, #7c3aed 50%, #a855f7 100%);
```

### Horizontal Bar
```css
background: linear-gradient(90deg, #0a2540, #635bff, #80e9ff);
```

---

## Typography

### Fonts
- **Display**: Inter (weights: 700, 800, 900)
- **Mono**: JetBrains Mono (weights: 400, 500, 600)

### Text Styles

| Style | Size | Weight | Letter Spacing |
|-------|------|--------|----------------|
| Hero Title | 120px | 900 | -4px |
| Section Title | 80px | 800 | -2px |
| Subtitle | 32px | 500 | 0 |
| Code | 24px | 500 | 0 |
| Pill/Badge | 18px | 600 | 1px |

---

## Components

### Cards

**Dark Card (Hero)**
```css
background: linear-gradient(135deg, #0a2540 0%, #1a365d 100%);
border-radius: 24px;
color: #ffffff;
```

**Accent Card (Purple Gradient)**
```css
background: linear-gradient(135deg, #635bff 0%, #7c3aed 50%, #a855f7 100%);
border-radius: 24px;
color: #ffffff;
```

**Light Card (Contrast)**
```css
background: #f6f9fc;
border-radius: 24px;
color: #0a2540;
```

### Pills/Badges
```css
background: rgba(255, 255, 255, 0.2);  /* on dark */
background: #635bff;                     /* on light */
color: #ffffff;
padding: 12px 24px;
border-radius: 100px;
font-family: 'JetBrains Mono';
font-weight: 600;
text-transform: uppercase;
```

### Code Blocks
```css
background: #0a2540;
color: #80e9ff;
padding: 16px 24px;
border-radius: 12px;
font-family: 'JetBrains Mono';
```

---

## Animation Guidelines

### Timing
- **Fast cuts**: 45-60 frames (1.5-2 seconds at 30fps)
- **Transition duration**: 8-10 frames
- **Text entrance**: Spring animation (damping: 14, stiffness: 200)

### Motion Principles
- Elements slide in from edges (usually bottom or right)
- Use spring animations for organic feel
- Scale up slightly on emphasis (1.0 → 1.05)
- Fade transitions between contrasting scenes

---

## Scene Types

1. **Hero Scene**: Midnight navy gradient + white text + cyan accents
2. **Feature Scene**: Purple gradient + white text + pill badges
3. **Contrast Scene**: Cloud white background + navy text + cyan code
4. **Terminal Scene**: Deep navy + cyan/green monospace text

---

---

## Brand

- **Website**: taskyou.dev
- **Install**: `curl -fsSL taskyou.dev/install.sh | bash`
- **Taglines**: "Worktrees that work", "Keep context, no craziness"

---

## Do's and Don'ts

### Do
- Use bold, confident typography
- Keep text minimal and punchy
- Use purple gradient for emphasis moments
- Use cyan for code/terminal elements
- Mix dark and light scenes for rhythm

### Don't
- Use more than 2-3 colors per scene
- Add unnecessary decorative elements
- Use thin or light font weights
- Make scenes too busy or cluttered
