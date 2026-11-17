"""Writes the README's animated wordmark, which swaps Lykeion for Λύκειον.

GitHub shows README images without scripts or web fonts, so the letters are
outlined from Source Serif 4 (SIL OFL 1.1) and animated with CSS inside the SVG.
Requires fontTools and brotli: pip install fonttools brotli
Run from the repository root after `npm ci --prefix apps/web`.
"""

from pathlib import Path

from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont
from fontTools.varLib.instancer import instantiateVariableFont

FONTS = Path('apps/web/node_modules/@fontsource-variable/source-serif-4/files')
OUT = Path('.github/readme')
LATIN, GREEK = 'Lykeion', 'Λύκειον'
SIZE, TRACKING, WEIGHT = 96, 0.03, 420
WIDTH, HEIGHT, BASELINE = 520, 150, 102
CYCLE, STAGGER, TRAVEL = 7.0, 0.06, 120
COLORS = {'light': '#132637', 'dark': '#e6edf3'}


def load(subset):
    font = TTFont(FONTS / f'source-serif-4-{subset}-wght-normal.woff2')
    return instantiateVariableFont(font, {'wght': WEIGHT})


def letters(font, word):
    glyphs, cmap = font.getGlyphSet(), font.getBestCmap()
    scale = SIZE / font['head'].unitsPerEm
    advances = [glyphs[cmap[ord(c)]].width * scale for c in word]
    total = sum(advances) + TRACKING * SIZE * (len(word) - 1)
    x, paths = (WIDTH - total) / 2, []
    for char, advance in zip(word, advances):
        pen = SVGPathPen(glyphs)
        glyphs[cmap[ord(char)]].draw(TransformPen(pen, (scale, 0, 0, -scale, x, BASELINE)))
        paths.append(pen.getCommands())
        x += advance + TRACKING * SIZE
    return paths


def keyframes(name, shown_first):
    # One cycle: hold, roll up to the other word, hold, roll up back again.
    hold, rolled, back, done = 38, 46, 88, 96
    if shown_first:
        steps = [(0, 0, 1), (hold, 0, 1), (rolled, -TRAVEL, 0), (rolled + 0.01, TRAVEL, 0),
                 (back, TRAVEL, 0), (done, 0, 1), (100, 0, 1)]
    else:
        steps = [(0, TRAVEL, 0), (hold, TRAVEL, 0), (rolled, 0, 1), (back, 0, 1),
                 (done, -TRAVEL, 0), (100, -TRAVEL, 0)]
    body = ''.join(
        f'{p}%{{transform:translateY({y}px);opacity:{o}}}' for p, y, o in steps
    )
    return f'@keyframes {name}{{{body}}}'


def svg(color, latin, greek):
    ease = 'cubic-bezier(.65,0,.25,1)'
    style = (
        f'path{{fill:{color}}}'
        f'.l,.g{{animation:{CYCLE}s {ease} infinite both}}'
        '.l{animation-name:latin}.g{animation-name:greek}'
        + keyframes('latin', True)
        + keyframes('greek', False)
        + ''.join(f'.d{i}{{animation-delay:{i * STAGGER:.2f}s}}' for i in range(len(latin)))
        + '@media (prefers-reduced-motion:reduce){.l,.g{animation:none}.g{opacity:0}}'
    )
    groups = ''.join(
        f'<path class="l d{i}" d="{a}"/><path class="g d{i}" d="{b}"/>'
        for i, (a, b) in enumerate(zip(latin, greek))
    )
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {WIDTH} {HEIGHT}" '
        f'width="{WIDTH}" height="{HEIGHT}" role="img" aria-label="Lykeion (Λύκειον)">'
        f'<title>Lykeion (Λύκειον)</title><style>{style}</style>'
        f'<clipPath id="c"><rect y="{BASELINE - 92}" width="{WIDTH}" height="{TRAVEL + 2}"/></clipPath>'
        f'<g clip-path="url(#c)">{groups}</g></svg>\n'
    )


def main():
    latin, greek = letters(load('latin'), LATIN), letters(load('greek'), GREEK)
    OUT.mkdir(parents=True, exist_ok=True)
    for theme, color in COLORS.items():
        (OUT / f'wordmark-{theme}.svg').write_text(svg(color, latin, greek), encoding='utf-8')


if __name__ == '__main__':
    main()
