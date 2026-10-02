"""Build the trellis session flow: the SVGs and the workbench page.

    python3 docs/trellis-flow/build.py

writes, next to this file:
  session.svg         colours as CSS variables (the workbench themes it)
  session-light.svg   colours resolved for a light page (README, GitHub light)
  session-dark.svg    colours resolved for a dark page (GitHub dark)
  workbench.html      the diagram with zoom, the layout check and export
"""
import json
import os
import re

from engine import Layout
from session_flow import LEGEND, TITLE, spec

HERE = os.path.dirname(os.path.abspath(__file__))

TOKENS = {
    'edge': '--cds-chart-axis', 'tint': '--cds-chart-reference-tint', 'ink': '--cds-text-primary',
    'quiet': '--cds-text-secondary', 'turn': '--cds-chart-categorical-1', 'loop': '--cds-chart-categorical-2',
    'accent': '--cds-chart-categorical-1', 'good': '--cds-chart-status-good', 'bad': '--cds-chart-status-critical',
    'step': '--cds-chart-status-warning',
}
LIGHT = {'--cds-chart-axis': '#6b7383', '--cds-chart-reference-tint': '#eef1f6', '--cds-text-primary': '#1a1f29',
         '--cds-text-secondary': '#5d6676', '--cds-chart-categorical-1': '#2c64d1', '--cds-chart-categorical-2': '#d27a14',
         '--cds-chart-status-good': '#24804c', '--cds-chart-status-critical': '#cc3d33', '--cds-chart-status-warning': '#c09000'}
DARK = {'--cds-chart-axis': '#8d96a7', '--cds-chart-reference-tint': '#232933', '--cds-text-primary': '#e5e8ee',
        '--cds-text-secondary': '#a0a9b8', '--cds-chart-categorical-1': '#7ea6f5', '--cds-chart-categorical-2': '#f0a04b',
        '--cds-chart-status-good': '#5cc48a', '--cds-chart-status-critical': '#f0786d', '--cds-chart-status-warning': '#e6c34a'}
FONT = '-apple-system, BlinkMacSystemFont, &quot;Segoe UI&quot;, Helvetica, Arial, sans-serif'
CAMEL = re.compile(r'\b(fontSize|fontWeight|textAnchor|strokeWidth|strokeDasharray|strokeLinecap|fillOpacity|markerEnd)=')


def to_svg(jsx, colours=None):
    body = jsx[jsx.index('return <svg') + len('return '):jsx.rindex('</svg>') + len('</svg>')]
    body = re.sub(r"=\{(\w+)\}", lambda m: "='" + (colours[TOKENS[m.group(1)]] if colours else f"var({TOKENS[m.group(1)]})") + "'", body)
    body = CAMEL.sub(lambda m: re.sub(r'[A-Z]', lambda c: '-' + c.group(0).lower(), m.group(1)) + '=', body)
    body = re.sub(r" data-claude-text-id='[^']*'", '', body)
    w, h = re.search(r"viewBox='0 0 (\d+) (\d+)'", body).groups()
    body = body.replace('<svg ', f"<svg xmlns='http://www.w3.org/2000/svg' width='{w}' height='{h}' font-family='{FONT}' ", 1)
    return body + '\n'


def main():
    jsx = Layout(spec).jsx(TITLE, LEGEND)
    out = {
        'session.svg': to_svg(jsx),
        'session-light.svg': to_svg(jsx, LIGHT),
        'session-dark.svg': to_svg(jsx, DARK),
    }
    for name, text in out.items():
        with open(os.path.join(HERE, name), 'w') as f:
            f.write(text)
    with open(os.path.join(HERE, 'workbench.template.html')) as f:
        page = f.read()
    entry = ("DIAGRAMS.push({ id: 'session', title: 'A session', "
             "note: \"Claude Code's hook lifecycle with each trellis flow at the hook that runs it\", "
             "render: () => document.importNode(new DOMParser().parseFromString(" + json.dumps(out['session.svg']) +
             ", 'image/svg+xml').documentElement, true) });")
    with open(os.path.join(HERE, 'workbench.html'), 'w') as f:
        f.write(page.replace('/*__DIAGRAMS__*/', entry))


if __name__ == '__main__':
    main()
