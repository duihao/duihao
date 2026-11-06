import 'package:flutter/material.dart';

/// 与桌面端 / 网页端同款的深色基调（--bg / --card / --acc 取自 shared.css）。
class DhColors {
  static const bg = Color(0xFF0F0F0F);
  static const card = Color(0xFF1A1A1A);
  static const cardHi = Color(0xFF232326);
  static const line = Color(0x24FFFFFF);
  static const tx = Color(0xFFF2F2F4);
  static const tx2 = Color(0xFFB9B9C0);
  static const mut = Color(0xFF8C8C96);
  static const acc = Color(0xFFFF4D8D);
  static const danger = Color(0xFFF87171);
  static const ok = Color(0xFF4ADE80);
}

ThemeData duihaoTheme() {
  final scheme = ColorScheme.fromSeed(
    seedColor: DhColors.acc,
    brightness: Brightness.dark,
  ).copyWith(
    primary: DhColors.acc,
    surface: DhColors.card,
    error: DhColors.danger,
  );
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    scaffoldBackgroundColor: DhColors.bg,
    appBarTheme: const AppBarTheme(
      backgroundColor: DhColors.bg,
      foregroundColor: DhColors.tx,
      elevation: 0,
      centerTitle: false,
      titleTextStyle: TextStyle(
        color: DhColors.tx,
        fontSize: 17,
        fontWeight: FontWeight.w600,
      ),
    ),
    cardColor: DhColors.card,
    dividerColor: DhColors.line,
    listTileTheme: const ListTileThemeData(
      iconColor: DhColors.tx2,
      textColor: DhColors.tx,
    ),
    snackBarTheme: const SnackBarThemeData(
      backgroundColor: DhColors.cardHi,
      contentTextStyle: TextStyle(color: DhColors.tx),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: DhColors.card,
      hintStyle: const TextStyle(color: DhColors.mut),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: const BorderSide(color: DhColors.line),
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: const BorderSide(color: DhColors.line),
      ),
      focusedBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: const BorderSide(color: DhColors.acc),
      ),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        backgroundColor: DhColors.acc,
        foregroundColor: Colors.white,
        minimumSize: const Size.fromHeight(46),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        textStyle: const TextStyle(fontSize: 15, fontWeight: FontWeight.w600),
      ),
    ),
  );
}

/// 把字节数说成人话：手机屏幕窄，不显示长小数。
String humanSize(int bytes) {
  if (bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var v = bytes.toDouble();
  var i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  final s = v >= 100 || i == 0 ? v.toStringAsFixed(0) : v.toStringAsFixed(1);
  return '$s ${units[i]}';
}

String humanDate(int unix) {
  if (unix <= 0) return '';
  final d = DateTime.fromMillisecondsSinceEpoch(unix * 1000);
  String two(int n) => n.toString().padLeft(2, '0');
  return '${d.year}-${two(d.month)}-${two(d.day)} ${two(d.hour)}:${two(d.minute)}';
}
