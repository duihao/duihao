import 'package:flutter/material.dart';

import 'theme.dart';

/// 缩略图加载失败 / 不支持的格式（HEIC）时的占位。
/// 不做「重试」按钮：一张图裂了就裂了，划过去就好。
class ThumbFallback extends StatelessWidget {
  const ThumbFallback({super.key, this.label});

  final String? label;

  @override
  Widget build(BuildContext context) {
    return Container(
      color: DhColors.cardHi,
      alignment: Alignment.center,
      child: label == null
          ? const Icon(Icons.image_not_supported_outlined,
              size: 18, color: DhColors.mut)
          : Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                const Icon(Icons.image_outlined, size: 16, color: DhColors.mut),
                const SizedBox(height: 3),
                Text(label!,
                    style: const TextStyle(fontSize: 9.5, color: DhColors.mut)),
              ],
            ),
    );
  }
}
