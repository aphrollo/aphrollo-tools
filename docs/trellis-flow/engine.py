"""Lay out a top-to-bottom flow spec and emit it as JSX (an SVG).

The spec is a list of spine items. Each item sits on one vertical spine; a
diamond's side branch goes to the right lane (or skips down on the left), and
every jump that is not the next item is a goto, routed on its own rail:
  - right rails carry gotos downward into a later spine item,
  - the orange rail carries loop-backs to the loop's re-entry diamond,
  - far-right rails carry the few gotos that go up (resume, update),
  - left rails carry skips ("no" past a block) downward.
A goto joins its target just above it, on the spine arrow.
"""
import html

W = 1100
CX = 560            # spine centre
SPINE_W = 320       # spine box width
LANE_X0, LANE_X1 = 794, 966
SIDE_X0, SIDE_X1 = 16, 166
LOOP_X = 1044       # orange loop rail
GAP = 26

CHAR = {11.5: 6.1, 11: 5.9, 12: 6.5, 13: 7.1, '13b': 7.7, '12b': 7.1, 10.5: 5.7}


def tw(s, size=11.5, bold=False):
    k = f'{int(size)}b' if bold else size
    return len(s) * CHAR.get(k, size * 0.53)


def wrap(text, width, size=11.5, bold=False):
    out, line = [], ''
    for word in text.split(' '):
        cand = (line + ' ' + word).strip()
        if line and tw(cand, size, bold) > width:
            out.append(line)
            line = word
        else:
            line = cand
    if line:
        out.append(line)
    return out


def esc(s):
    return html.escape(s, quote=False).replace('{', '&#123;').replace('}', '&#125;')


# ---------- spec constructors ----------
def P(text, sub=None, tone=None, id=None, stop=False):
    return dict(k='pill', text=text, sub=sub, tone=tone, id=id, stop=stop)


def H(text, tone='cc', sub=None, id=None, side=(), stop=False, to=None):
    return dict(k='hook', text=text, tone=tone, sub=sub, id=id, side=list(side), stop=stop or bool(to), to=to)


def B(text, sub=(), id=None, to=None, outside=False, shade=False):
    return dict(k='box', text=text, sub=list(sub), id=id, to=to, outside=outside, shade=shade)


def HO(text, id=None, note=None):
    return dict(k='handover', text=text, id=id, note=note)


def D(q, main, side=None, id=None, note=None):
    return dict(k='diamond', q=q, main=main, side=side, id=id, note=note)


def S(label, items=(), to=None, left=False):
    """A diamond's side branch: lane items, then where it goes (an id, 'loop', or None for an end pill)."""
    return dict(label=label, items=list(items), to=to, left=left)


def SE(text, sub=None, free=False):
    return dict(text=text, sub=sub, free=free)


def F(title, items, tone=None, id=None):
    return dict(k='frame', title=title, items=items, tone=tone, id=id)


# ---------- layout ----------
class Layout:
    def __init__(self, spec):
        self.spec = spec
        self.out = []          # (layer, jsx)
        self.nodes = {}        # id -> geometry
        self.gotos = []        # pending routes
        self.y = 0
        self.lane_free = 0     # lowest y the lane is free below
        self.n = 0
        self.frames = []
        self.depth = 0
        self.right_targets = set()
        self.all_targets = set()

        def scan(items):
            for it in items:
                if it['k'] == 'frame':
                    scan(it['items'])
                    continue
                if it.get('to') and it['to'] != 'loop':
                    self.right_targets.add(it['to'])
                    self.all_targets.add(it['to'])
                sd = it.get('side') if it['k'] == 'diamond' else None
                if sd and sd.get('to') and sd['to'] != 'loop':
                    self.all_targets.add(sd['to'])
                    if not sd['left']:
                        self.right_targets.add(sd['to'])
        scan(spec)
        self.right_targets.add('call')

    def uid(self, base='n'):
        self.n += 1
        return f'{base}{self.n}'

    def emit(self, s, layer=2):
        self.out.append((layer, s))

    # sizes
    def box_lines(self, it, width):
        title = wrap(it['text'], width - 32, 13, True)
        joined = ' '.join(it.get('sub') or [])
        sub = wrap(joined, width - 32) if joined else []
        return title, sub

    def box_h(self, title, sub):
        if not sub and len(title) == 1:
            return 40
        return 16 + 18 * len(title) + 16 * len(sub) + (6 if sub else 0) + 4

    def diamond_geom(self, q):
        lines = wrap(q, 150, 12)
        w = max(tw(l, 12) for l in lines)
        hh = 32 if len(lines) == 1 else (40 if len(lines) == 2 else 48)
        f = 1 - (7 + 8 * (len(lines) - 1)) / hh
        hw = max(62, int((w + 14) / (2 * f)) + 1)
        return lines, hw, hh

    def place(self, items, depth):
        prev_flows = True
        for it in items:
            k = it['k']
            if k == 'frame':
                self.place_frame(it, depth)
                prev_flows = it.get('flows_out', True)
                continue
            if it.get('id') in self.all_targets:
                self.y += 12
            if it.get('id') in self.right_targets:
                self.y = max(self.y, self.lane_free + 16)
            top = self.y
            self.depth = depth
            label = self.prev_label
            self.prev_label = None
            g = self.place_item(it, top, depth)
            if prev_flows and self.prev_bottom is not None:
                self.arrow(self.prev_bottom, g['top'], label)
            self.prev_bottom = g['bottom']
            prev_flows = self.flows_out(it)
            if not prev_flows:
                self.prev_bottom = None
            if it.get('id'):
                self.nodes[it['id']] = g
            self.y = g['bottom'] + GAP
        self.last_flows = prev_flows

    def flows_out(self, it):
        if it['k'] == 'box' and it.get('to'):
            return False
        if it['k'] == 'pill' and it.get('stop'):
            return False
        if it['k'] == 'hook' and it.get('stop'):
            return False
        if it['k'] == 'diamond' and it['main'] is None:
            return False
        return True

    def place_frame(self, fr, depth):
        y0 = self.y
        x0 = 270 + 8 * depth if fr.get('tone') is None else (186 if fr['tone'] == 'turn' else 196)
        x1 = 992 - 5 * depth if fr.get('tone') is None else (1034 if fr['tone'] == 'turn' else 1028)
        self.y += 42
        self.place(fr['items'], depth + 1)
        fr['flows_out'] = self.last_flows
        y1 = max(self.y - GAP + 26, self.lane_free + 8)
        self.y = y1 + GAP
        color = {'turn': '{turn}', 'loop': '{loop}'}.get(fr.get('tone'), '{edge}')
        anchor = fr.get('id') or fr['title'].lower().replace(' ', '-').replace(',', '')
        self.emit(f"<g data-claude-anchor='f-{anchor}'><rect x='{x0}' y='{y0}' width='{x1 - x0}' height='{y1 - y0}' rx='10' fill='none' stroke={color} strokeWidth='{1.25 if fr.get('tone') else 1}' strokeDasharray='5 4'/>"
                  f"<text data-claude-text-id='f-{anchor}' x='{x0 + 12}' y='{y0 + 20}' fontSize='11.5' fontWeight='600' fill={color if fr.get('tone') else '{quiet}'}>{esc(fr['title'])}</text></g>", 0)

    def arrow(self, y0, y1, label=None, x=CX):
        self.emit(f"<path d='M{x} {y0}V{y1 - 2}' fill='none' stroke={{edge}} strokeWidth='1.25' markerEnd='url(#fa)'/>", 1)
        if label:
            self.emit(f"<text x='{x + 9}' y='{y0 + 15}' fontSize='11.5' fill={{quiet}}>{esc(label)}</text>", 3)

    def text_block(self, x, y, title, sub, anchor='start', size=13, bold=True):
        s = ''
        ty = y
        for i, t in enumerate(title):
            ty = y + 21 + 18 * i
            s += f"<text x='{x}' y='{ty}' textAnchor='{anchor}' fontWeight='{600 if bold else 400}' fill={{ink}}>{esc(t)}</text>"
        sy = ty + (21 if sub else 0)
        for i, t in enumerate(sub):
            s += f"<text x='{x}' y='{sy + 16 * i}' textAnchor='{anchor}' fontSize='11.5' fill={{quiet}}>{esc(t)}</text>"
        return s

    def place_item(self, it, top, depth):
        k = it['k']
        aid = it.get('id') or self.uid(k)
        if k in ('pill', 'handover'):
            w = max(150, int(tw(it['text'], 13, True)) + 44)
            fill, stroke, sw = "'none'", '{edge}', 1.25
            if k == 'handover':
                fill, stroke, sw = '{accent}', '{accent}', 2
            elif it.get('tone') == 'good':
                fill, stroke, sw = '{good}', '{good}', 1.5
            op = " fillOpacity='0.13'" if fill != "'none'" else ''
            s = f"<g data-claude-anchor='{aid}'><rect x='{CX - w // 2}' y='{top}' width='{w}' height='40' rx='20' fill={fill}{op} stroke={stroke} strokeWidth='{sw}'/><text x='{CX}' y='{top + 25}' textAnchor='middle' fontWeight='600' fill={{ink}}>{esc(it['text'])}</text></g>"
            self.emit(s)
            if it.get('sub'):
                lines = wrap(it['sub'], 200)
                t = ''.join(f"<text x='{CX + w // 2 + 12}' y='{top + 16 + 15 * i}' fontSize='11.5' fill={{quiet}}>{esc(l)}</text>" for i, l in enumerate(lines))
                self.emit(t, 3)
            if it.get('note'):
                self.lane_note(it['note'], top)
            return dict(top=top, bottom=top + 40, cy=top + 20, left=CX - w // 2, right=CX + w // 2)
        if k == 'hook':
            w = 300
            tone = it.get('tone')
            fill, stroke, sw = {'cc': ('{tint}', '{edge}', 1.25), 'step': ('{step}', '{step}', 1.5), 'tool': ('{turn}', '{turn}', 1.5), 'bad': ('{bad}', '{bad}', 1.5)}[tone]
            op = " fillOpacity='0.14'" if tone != 'cc' else ''
            h = 40 if not it.get('sub') else 52
            if it.get('to'):
                top = max(top, self.lane_free - h // 2 + 4)
            s = f"<g data-claude-anchor='{aid}'><rect x='{CX - w // 2}' y='{top}' width='{w}' height='{h}' rx='8' fill={fill}{op} stroke={stroke} strokeWidth='{sw}'/><text x='{CX}' y='{top + 25 if h == 40 else top + 22}' textAnchor='middle' fontWeight='600' fill={{ink}}>{esc(it['text'])}</text>"
            if it.get('sub'):
                s += f"<text x='{CX}' y='{top + 40}' textAnchor='middle' fontSize='11' fill={{quiet}}>{esc(it['sub'])}</text>"
            self.emit(s + '</g>')
            g = dict(top=top, bottom=top + h, cy=top + h // 2, left=CX - w // 2, right=CX + w // 2)
            self.side_events(it.get('side') or [], g)
            if it.get('to'):
                self.goto_from(g['right'], g['cy'], it['to'], g)
            return g
        if k == 'box':
            title, sub = self.box_lines(it, SPINE_W)
            h = self.box_h(title, sub)
            if it.get('to'):
                top = max(top, self.lane_free - h // 2 + 4)
            x0 = CX - SPINE_W // 2
            dash = " strokeDasharray='0.5 3.5' strokeLinecap='round' strokeWidth='1.6'" if it.get('outside') else " strokeWidth='1.25'"
            fill = '{tint}' if it.get('shade') else "'none'"
            s = f"<g data-claude-anchor='{aid}'><rect x='{x0}' y='{top}' width='{SPINE_W}' height='{h}' rx='8' fill={fill} stroke={{edge}}{dash}/>"
            s += self.text_block(x0 + 16, top, title, sub) + '</g>'
            if len(title) == 1 and not sub:
                s = s.replace(f"y='{top + 21}'", f"y='{top + 25}'", 1)
            self.emit(s)
            g = dict(top=top, bottom=top + h, cy=top + h // 2, left=x0, right=x0 + SPINE_W)
            if it.get('to'):
                self.goto_from(g['right'], g['cy'], it['to'], g, from_spine=True)
            return g
        if k == 'diamond':
            return self.place_diamond(it, top, aid)
        raise ValueError(k)

    def lane_note(self, text, top):
        lines = []
        for t in text if isinstance(text, list) else [text]:
            lines += wrap(t, LANE_X1 - LANE_X0 - 24)
        h = 14 + 16 * len(lines)
        top = max(top, self.lane_free)
        s = f"<g><rect x='{LANE_X0}' y='{top}' width='{LANE_X1 - LANE_X0}' height='{h}' rx='8' fill={{tint}} stroke={{edge}} strokeWidth='1'/>"
        s += ''.join(f"<text x='{LANE_X0 + 12}' y='{top + 19 + 16 * i}' fontSize='11.5' fill={{ink}}>{esc(l)}</text>" for i, l in enumerate(lines))
        self.emit(s + '</g>')
        self.lane_free = top + h + 14

    def side_events(self, ses, g):
        y = g['top']
        if ses and all(se.get('free') for se in ses):
            self.emit(f"<text x='{SIDE_X0}' y='{y - 6}' fontSize='11' fill={{quiet}}>any time; trellis takes no action</text>", 3)
        for se in ses:
            h = 40 if not se.get('sub') else 44
            subl = wrap(se['sub'], 148, 10.5) if se.get('sub') else []
            h = 26 + 13 * len(subl) if subl else 34
            s = f"<g><rect x='{SIDE_X0}' y='{y}' width='{SIDE_X1 - SIDE_X0}' height='{h}' rx='8' fill='none' stroke={{edge}} strokeWidth='1.25' strokeDasharray='4 3'/>"
            s += f"<text x='{(SIDE_X0 + SIDE_X1) // 2}' y='{y + (21 if not subl else 17)}' textAnchor='middle' fontSize='12' fill={{ink}}>{esc(se['text'])}</text>"
            s += ''.join(f"<text x='{(SIDE_X0 + SIDE_X1) // 2}' y='{y + 31 + 13 * i}' textAnchor='middle' fontSize='10.5' fill={{quiet}}>{esc(l)}</text>" for i, l in enumerate(subl))
            self.emit(s + '</g>')
            cy = y + h // 2
            if se.get('free'):
                y += h + 8
                continue
            self.emit(f"<path d='M{g['left']} {g['cy']}H{SIDE_X1 + 12}V{cy}H{SIDE_X1 + 2}' fill='none' stroke={{edge}} strokeWidth='1.25' strokeDasharray='4 3' markerEnd='url(#fa)'/>", 1)
            y += h + 8
        self.side_bottom = y

    def place_diamond(self, it, top, aid):
        lines, hw, hh = self.diamond_geom(it['q'])
        side = it.get('side')
        if side and not side['left']:
            # the lane must be free where the branch starts
            need = self.lane_free - (hh - 20)
            if top < need:
                top = need
        cy = top + hh
        pts = f"{CX - hw},{cy} {CX},{cy - hh} {CX + hw},{cy} {CX},{cy + hh}"
        s = f"<g data-claude-anchor='{aid}'><polygon points='{pts}' fill='none' stroke={{edge}} strokeWidth='1.25'/>"
        y0 = cy + 4 - 8 * (len(lines) - 1)
        s += ''.join(f"<text x='{CX}' y='{y0 + 16 * i}' textAnchor='middle' fontSize='12' fill={{ink}}>{esc(l)}</text>" for i, l in enumerate(lines))
        self.emit(s + '</g>')
        g = dict(top=top, bottom=cy + hh, cy=cy, left=CX - hw, right=CX + hw, diamond=True)
        if it.get('note'):
            room = (CX - hw - 12) - (270 + 8 * self.depth + 10)
            nl = wrap(it['note'], max(110, room))
            self.emit(''.join(f"<text x='{CX - hw - 12}' y='{cy - 30 + 15 * i}' textAnchor='end' fontSize='11' fill={{quiet}}>{esc(l)}</text>" for i, l in enumerate(nl)), 3)
        self.prev_label = it['main']
        if side:
            if side['left']:
                self.gotos.append(dict(kind='left', x0=CX - hw, y0=cy, to=side['to'], label=side['label']))
            elif side['items']:
                self.place_lane(side, g)
            else:
                self.goto_from(CX + hw, cy, side['to'], g, label=side['label'])
        return g

    def place_lane(self, side, g):
        y = g['cy']
        first = True
        prev = None
        for it in side['items']:
            if it['k'] == 'pill':
                w = LANE_X1 - LANE_X0
                lines = wrap(it['text'], w - 30, 13, True)
                sub = wrap(it['sub'], w - 30) if it.get('sub') else []
                h = 22 + 18 * len(lines) + 16 * len(sub)
                top = y - 20 if first else y
                s = f"<g><rect x='{LANE_X0}' y='{top}' width='{w}' height='{h}' rx='20' fill='none' stroke={{edge}} strokeWidth='1.25'/>"
                s += self.text_block((LANE_X0 + LANE_X1) // 2, top - 3, lines, sub, anchor='middle')
                self.emit(s + '</g>')
                bx = dict(top=top, bottom=top + h, cy=top + 20 if first else top + h // 2, right=LANE_X1)
            else:
                title, sub = self.box_lines(it, LANE_X1 - LANE_X0)
                h = self.box_h(title, sub)
                top = y - 20 if first else y
                tone = it.get('tone')
                if it['k'] == 'hook':
                    fill = "{step} fillOpacity='0.14'"
                    stroke = '{step}'
                else:
                    fill, stroke = "'none'", '{edge}'
                dash = " strokeDasharray='0.5 3.5' strokeLinecap='round' strokeWidth='1.6'" if it.get('outside') else " strokeWidth='1.25'"
                s = f"<g><rect x='{LANE_X0}' y='{top}' width='{LANE_X1 - LANE_X0}' height='{h}' rx='8' fill={fill} stroke={stroke}{dash}/>"
                s += self.text_block(LANE_X0 + 14, top, title, sub)
                if len(title) == 1 and not sub:
                    s = s.replace(f"y='{top + 21}'", f"y='{top + 25}'", 1)
                self.emit(s + '</g>')
                bx = dict(top=top, bottom=top + h, cy=top + 20, right=LANE_X1)
            if first:
                self.emit(f"<path d='M{g['right']} {g['cy']}H{LANE_X0 - 2}' fill='none' stroke={{edge}} strokeWidth='1.25' markerEnd='url(#fa)'/>", 1)
                if side['label']:
                    self.emit(f"<text x='{(g['right'] + LANE_X0) // 2}' y='{g['cy'] - 7}' textAnchor='middle' fontSize='11.5' fill={{quiet}}>{esc(side['label'])}</text>", 3)
            else:
                cxl = (LANE_X0 + LANE_X1) // 2
                self.emit(f"<path d='M{cxl} {prev['bottom']}V{top - 2}' fill='none' stroke={{edge}} strokeWidth='1.25' markerEnd='url(#fa)'/>", 1)
            prev = bx
            first = False
            y = bx['bottom'] + 22
        self.lane_free = prev['bottom'] + 22
        if side['to']:
            self.goto_from(LANE_X1, prev['cy'], side['to'], prev)

    def goto_from(self, x, y, to, g, label=None, from_spine=False):
        self.gotos.append(dict(kind='right', x0=x, y0=y, to=to, label=label, src_bottom=g['bottom']))

    # ---------- routing ----------
    def route(self):
        rails_right = []   # (x, y0, y1)
        rails_left = []
        up_rails = []
        entries = {}
        loop_target = self.nodes.get('call')
        for gt in self.gotos:
            to = gt['to']
            if to == 'loop':
                t = loop_target
                path = f"M{gt['x0']} {gt['y0']}H{LOOP_X}V{t['cy']}H{t['right'] + 2}"
                self.emit(f"<path d='{path}' fill='none' stroke={{loop}} strokeWidth='1.25' markerEnd='url(#fl)'/>", 1)
                continue
            t = self.nodes[to]
            jy = t['top'] - 12
            if gt['kind'] == 'left':
                span = (min(gt['y0'], jy), max(gt['y0'], jy))
                x = self.pick(rails_left, span, [258 - 9 * i for i in range(6)])
                path = f"M{gt['x0']} {gt['y0']}H{x}V{jy}H{CX}"
                self.emit(f"<path d='{path}' fill='none' stroke={{edge}} strokeWidth='1.25'/>", 1)
                if gt.get('label'):
                    self.emit(f"<text x='{(gt['x0'] + x) // 2}' y='{gt['y0'] + 15}' textAnchor='middle' fontSize='11.5' fill={{quiet}}>{esc(gt['label'])}</text>", 3)
                entries.setdefault(to, []).append(jy)
                continue
            if jy > gt['y0']:
                span = (gt['y0'], jy)
                x = self.pick(rails_right, span, [998 + 8 * i for i in range(5)], share=to)
                path = f"M{gt['x0']} {gt['y0']}H{x}V{jy}H{CX}"
                dash = ''
            else:
                x = self.pick(up_rails, (jy, gt['y0']), [1090 - 8 * i for i in range(6)], share=to)
                path = f"M{gt['x0']} {gt['y0']}H{x}V{jy}H{CX}"
                dash = " strokeDasharray='5 4'"
            self.emit(f"<path d='{path}' fill='none' stroke={{edge}} strokeWidth='1.25'{dash}/>", 1)
            if gt.get('label'):
                lx = (gt['x0'] + min(x, LANE_X0)) // 2 if gt['x0'] < LANE_X0 else gt['x0'] + 30
                self.emit(f"<text x='{lx}' y='{gt['y0'] - 7}' textAnchor='middle' fontSize='11.5' fill={{quiet}}>{esc(gt['label'])}</text>", 3)
            entries.setdefault(to, []).append(jy)
        # make sure every goto target has an arrow into it from its join
        for to, ys in entries.items():
            t = self.nodes[to]
            if not t.get('has_in'):
                self.emit(f"<path d='M{CX} {min(ys)}V{t['top'] - 2}' fill='none' stroke={{edge}} strokeWidth='1.25' markerEnd='url(#fa)'/>", 1)

    def pick(self, rails, span, xs, share=None):
        for x in xs:
            ok = True
            for (rx, a, b, tgt) in rails:
                if rx == x and not (span[1] < a - 6 or span[0] > b + 6) and not (share and tgt == share):
                    ok = False
                    break
            if ok:
                rails.append((x, span[0], span[1], share))
                return x
        rails.append((xs[-1], span[0], span[1], share))
        return xs[-1]

    def run(self):
        self.prev_bottom = None
        self.prev_label = None
        self.y = 70
        self.orig_arrow = self.arrow
        incoming = set()

        def arrow(y0, y1, label=None, x=CX):
            for nid, g in self.nodes.items():
                pass
            self.orig_arrow(y0, y1, label, x)
            self.last_arrow_to = y1
        self.arrow = arrow
        self.place(self.spec, 0)
        # mark nodes that already have an incoming spine arrow
        arrows = [s for (_, s) in self.out if s.startswith(f"<path d='M{CX} ")]
        ends = set()
        import re
        for a in arrows:
            m = re.match(rf"<path d='M{CX} (\d+)V(\d+)'", a)
            if m:
                ends.add(int(m.group(2)) + 2)
        for g in self.nodes.values():
            g['has_in'] = g['top'] in ends
        self.route()
        return max(self.y, self.lane_free) + 20

    def jsx(self, title, legend):
        height = self.run()
        body = ''.join(s for _, s in sorted(self.out, key=lambda t: t[0]))
        head = ("export default () => { const edge = 'var(--cds-chart-axis)', tint = 'var(--cds-chart-reference-tint)', ink = 'var(--cds-text-primary)', quiet = 'var(--cds-text-secondary)', turn = 'var(--cds-chart-categorical-1)', loop = 'var(--cds-chart-categorical-2)', accent = 'var(--cds-chart-categorical-1)', good = 'var(--cds-chart-status-good)', bad = 'var(--cds-chart-status-critical)', step = 'var(--cds-chart-status-warning)'; "
                f"return <svg viewBox='0 0 {W} {height}' role='img' aria-label=\"{esc(title)}\" fontSize='13'>"
                "<defs><marker id='fa' viewBox='0 0 10 10' refX='9' refY='5' markerWidth='6' markerHeight='6' orient='auto-start-reverse'><path d='M0 0L10 5L0 10z' fill={edge}/></marker>"
                "<marker id='fl' viewBox='0 0 10 10' refX='9' refY='5' markerWidth='6' markerHeight='6' orient='auto-start-reverse'><path d='M0 0L10 5L0 10z' fill={loop}/></marker></defs>"
                f"<text x='24' y='34' fontSize='16' fontWeight='600' fill={{ink}}>{esc(title)}</text>"
                f"<text x='24' y='54' fontSize='11.5' fill={{quiet}}>{esc(legend)}</text>")
        # give every text a stable id so labels stay editable in place
        import re
        count = {'n': 0}

        def tid(m):
            count['n'] += 1
            return f"<text data-claude-text-id='t{count['n']}' "
        body = re.sub(r"<text (?!data-claude-text-id)", tid, body)
        return head + body + '</svg>; };\n'
