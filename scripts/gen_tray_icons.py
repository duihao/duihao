#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""
托盘图标派生：把母图 logo 按目标像素**等比例缩放**，不做任何再设计。

母图 build/appicon.png 就是 E:/workbudy/duihao/logo.png 的等比例放大版（逐像素
比对差异为 0），所以这里缩放出来的托盘图标，和窗口标题栏 logo（/favicon.png）、
exe 图标（windows/icon.ico）是同一张图的不同尺寸，形状不会走样。

历史教训：早期版本在小尺寸上做了「亮色前景形态学加粗 + alpha 硬收紧」，16px
的勾被撑粗成另一副样子，托盘图标和窗口 logo 摆在一起像两个不同的应用。像素
级"优化"不如忠实缩放——现在只用高质量的等比缩放，别的都不做。

输出：
  build/tray/tray-{16,20,24,28,32,36,40,48,64}.png  Windows 托盘图集（按 DPI 精确取）
  build/appicon_tray.png (64px)                     macOS/Linux 托盘
  frontend/public/favicon.png (256px)               WebView 页面图标 / 窗口标题栏 logo

尺寸与 Windows 托盘的对应关系（16 DIP × 系统缩放比）：
  16=100% 20=125% 24=150% 28=175% 32=200% 36=225% 40=250% 48=300% 64=400%
28 和 36 两档以前缺失，运行时只能用 64px 兜底降采样，笔画会糊。

注意：build/appicon_tray_template.png（macOS 菜单栏模板图：黑底盘 + 勾挖空）不由本
脚本生成，它是单色版，随仓库一起维护，macOS 分支做好后再一起调整。

用法：
  python scripts/gen_tray_icons.py             # 全量生成
  python scripts/gen_tray_icons.py --preview   # 另出各尺寸对比条
"""
import os
import sys
from PIL import Image

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MASTER = os.path.join(ROOT, "build", "appicon.png")
OUT_DIR = os.path.join(ROOT, "build", "tray")
FAVICON = os.path.join(ROOT, "frontend", "public", "favicon.png")

SIZES = (16, 20, 24, 28, 32, 36, 40, 48, 64)
FAVICON_SIZE = 256

# 缩放分两步：先 LANCZOS 缩到 4 倍目标尺寸（保留整体轮廓），再 BOX 降到目标
# 尺寸（面积平均，抗锯齿边不会出现振铃光晕）。一步 LANCZOS 到 16px 会在勾的
# 尖端留下亮边，缩得太狠时 BOX 反而更稳。
SUPERSAMPLE = 4


def load_master():
    """母图按 alpha 裁掉四周纯透明留白，再居中补成正方形。"""
    im = Image.open(MASTER).convert("RGBA")
    bbox = im.split()[3].getbbox()
    if bbox:
        im = im.crop(bbox)
    w, h = im.size
    if w != h:
        s = max(w, h)
        sq = Image.new("RGBA", (s, s), (0, 0, 0, 0))
        sq.paste(im, ((s - w) // 2, (s - h) // 2))
        im = sq
    return im


def derive(size, master):
    """母图 → size px，等比缩放，保持原图形与比例。"""
    if master.width == size:
        return master.copy()
    mid = size * SUPERSAMPLE
    if master.width > mid:
        return master.resize((mid, mid), Image.LANCZOS).resize((size, size), Image.BOX)
    return master.resize((size, size), Image.LANCZOS)


def preview_strip(icons, scale=10, bg=(255, 255, 255), gap=16):
    cells = []
    for im in icons:
        big = im.resize((im.width * scale, im.height * scale), Image.NEAREST)
        cell = Image.new("RGBA", (big.width + gap * 2, big.height + gap * 2), bg + (255,))
        cell.paste(big, (gap, gap), big)
        cells.append(cell)
    out = Image.new("RGBA", (sum(c.width for c in cells), max(c.height for c in cells)), bg + (255,))
    x = 0
    for c in cells:
        out.paste(c, (x, 0), c)
        x += c.width
    return out


def main():
    master = load_master()
    print("母图:", os.path.relpath(MASTER, ROOT), master.size,
          "-> 等比缩放到", len(SIZES), "个托盘尺寸")

    os.makedirs(OUT_DIR, exist_ok=True)
    icons = {}
    for sz in SIZES:
        icons[sz] = derive(sz, master)
        p = os.path.join(OUT_DIR, "tray-%02d.png" % sz)
        icons[sz].save(p)
        print("  ->", os.path.relpath(p, ROOT), "%dx%d" % (sz, sz))

    tray64 = os.path.join(ROOT, "build", "appicon_tray.png")
    derive(64, master).save(tray64)
    print("  ->", os.path.relpath(tray64, ROOT), "64x64 (macOS/Linux 托盘)")

    os.makedirs(os.path.dirname(FAVICON), exist_ok=True)
    derive(FAVICON_SIZE, master).save(FAVICON)
    print("  ->", os.path.relpath(FAVICON, ROOT), "%dx%d (favicon/窗口 logo)"
          % (FAVICON_SIZE, FAVICON_SIZE))

    if "--preview" in sys.argv:
        out = os.path.join(ROOT, "build", "tray-preview.png")
        preview_strip([icons[s] for s in (16, 20, 24, 32, 48, 64)], scale=8).save(out)
        print("预览 ->", os.path.relpath(out, ROOT))
    return 0


if __name__ == "__main__":
    sys.exit(main())
