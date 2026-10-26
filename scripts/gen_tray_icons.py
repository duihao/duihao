#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""
从母图 build/appicon.png 派生全套托盘图标（Windows 托盘图集 + mac/Linux 托盘 + favicon）。

母图是 1024×1024 的大图，直接缩小到 16/24px 会发虚。本脚本在派生时做三件
自动适配（只影响派生物，不动母图）：

  1. 裁边成方       —— 按不透明像素 bbox 裁剪，去掉设计稿四周留白
  2. 边缘收紧       —— 半透明抗锯齿边在小尺寸下呈灰边，按 alpha 下限重新拉满
  3. 亮色前景加粗   —— 母图中的亮色主体（如白色对勾）在小尺寸下笔画变细，
                       按目标尺寸比例做形态学膨胀（母图无亮色主体时自动跳过）

输出：
  build/tray/tray-{16,20,24,32,40,48,64}.png   Windows 托盘图集（按 DPI 精确取）
  build/appicon_tray.png (64px)                macOS/Linux 托盘
  frontend/public/favicon.png (256px)          WebView 页面图标

用法：
  python scripts/gen_tray_icons.py            # 全量生成
  python scripts/gen_tray_icons.py --preview  # 另出「直缩 vs 适配」对比图
"""
import os
import sys
from PIL import Image, ImageFilter

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MASTER = os.path.join(ROOT, "build", "appicon.png")
OUT_DIR = os.path.join(ROOT, "build", "tray")
FAVICON = os.path.join(ROOT, "frontend", "public", "favicon.png")

SIZES = (16, 20, 24, 32, 40, 48, 64)
FAVICON_SIZE = 256
SS = 8                  # 超采样倍数：先缩到 size*8 再做像素级处理，最后降到 size

# 小尺寸适配参数（在 size*8 的中间画布上生效）
ALPHA_FLOOR = 40        # alpha 低于此值视为背景，其余重新拉满（去灰边）
LIGHT_THRESHOLD = 200   # RGB 均 > 此值视为"亮色前景"（对勾类）
THICKEN_RATIO = 0.055   # 前景加粗量，占画布比例（仅 size<32 生效）


def load_master():
    im = Image.open(MASTER).convert("RGBA")
    bbox = im.split()[3].getbbox()
    if bbox:
        im = im.crop(bbox)
    # 居中补成正方形（正常母图本就是方形，此处只是防御）
    w, h = im.size
    if w != h:
        s = max(w, h)
        sq = Image.new("RGBA", (s, s), (0, 0, 0, 0))
        sq.paste(im, ((s - w) // 2, (s - h) // 2))
        im = sq
    return im


def tighten_alpha(im):
    """alpha 下限截断后线性拉满：半透明边缘变实色，小尺寸下不再发灰。"""
    r, g, b, a = im.split()
    lut = [0] * ALPHA_FLOOR + [min(255, (v - ALPHA_FLOOR) * 255 // (255 - ALPHA_FLOOR))
                               for v in range(ALPHA_FLOOR, 256)]
    return Image.merge("RGBA", (r, g, b, a.point(lut)))


def thicken_light(im, px):
    """把亮色前景（对勾类）形态学膨胀 px 像素（在当前分辨率上）。"""
    r, g, b, a = im.split()
    # 亮色且不透明的像素 = 前景 mask（逐像素扫描，分辨率低所以够快）
    w, h = im.size
    light = Image.new("L", im.size, 0)
    lp = light.load()
    rp, gp, bp, ap = r.load(), g.load(), b.load(), a.load()
    found = False
    for y in range(h):
        for x in range(w):
            if rp[x, y] > LIGHT_THRESHOLD and gp[x, y] > LIGHT_THRESHOLD \
                    and bp[x, y] > LIGHT_THRESHOLD and ap[x, y] > 150:
                lp[x, y] = 255
                found = True
    if not found:
        return im  # 母图没有亮色主体（如深色系 logo），跳过
    k = max(3, int(px) | 1)  # MaxFilter 需要奇数
    grown = light.filter(ImageFilter.MaxFilter(k))
    white = Image.new("RGBA", im.size, (255, 255, 255, 255))
    return Image.composite(white, im, grown)


def derive(size, master, adapt=True):
    """母图 → size px 托盘图标。"""
    mid = size * SS
    im = master.resize((mid, mid), Image.BOX)
    if adapt and size < 32:
        im = tighten_alpha(im)
        im = thicken_light(im, THICKEN_RATIO * mid)
    elif adapt and size < 64:
        im = tighten_alpha(im)
    return im.resize((size, size), Image.BOX)


def plain_resize(master, size):
    """对照组：母图直接 BOX 缩放（不做任何适配）。"""
    return master.resize((size, size), Image.BOX)


def composite_strip(icons, scale=12, bg=(32, 32, 32), gap=14):
    cells = []
    for im in icons:
        big = im.resize((im.width * scale, im.height * scale), Image.NEAREST)
        cell = Image.new("RGBA", (big.width + gap * 2, big.height + gap * 2), bg + (255,))
        cell.paste(big, (gap, gap), big)
        cells.append(cell)
    w = sum(c.width for c in cells)
    h = max(c.height for c in cells)
    out = Image.new("RGBA", (w, h), bg + (255,))
    x = 0
    for c in cells:
        out.paste(c, (x, 0), c)
        x += c.width
    return out


def main():
    master = load_master()
    print("母图:", os.path.relpath(MASTER, ROOT), master.size, "->", len(SIZES), "个托盘尺寸")

    os.makedirs(OUT_DIR, exist_ok=True)
    for sz in SIZES:
        p = os.path.join(OUT_DIR, "tray-%02d.png" % sz)
        derive(sz, master).save(p)
        print("  ->", os.path.relpath(p, ROOT), "%dx%d" % (sz, sz))

    tray64 = os.path.join(ROOT, "build", "appicon_tray.png")
    derive(64, master).save(tray64)
    print("  ->", os.path.relpath(tray64, ROOT), "64x64 (macOS/Linux 托盘)")

    os.makedirs(os.path.dirname(FAVICON), exist_ok=True)
    master.resize((FAVICON_SIZE, FAVICON_SIZE), Image.BOX).save(FAVICON)
    print("  ->", os.path.relpath(FAVICON, ROOT), "%dx%d (favicon)" % (FAVICON_SIZE, FAVICON_SIZE))

    if "--preview" in sys.argv:
        tiles = []
        for sz in (24, 16):
            tiles.append(plain_resize(master, sz))   # 直缩（旧行为）
            tiles.append(derive(sz, master))         # 适配（新行为）
        prev = composite_strip(tiles, scale=12)
        out = os.path.join(ROOT, "build", "tray-preview.png")
        prev.save(out)
        print("预览 ->", os.path.relpath(out, ROOT), prev.size, "(每组左=直缩 右=适配)")


if __name__ == "__main__":
    main()
