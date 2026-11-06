# 对号手机端（Flutter 薄壳）

P1 阶段的客户端：接自家 NAS，看照片、看片、翻文件。不是「另一个网盘」，
是一扇窗——数据始终在对号这台机器上。

## 现在的状态

| 能力 | 状态 |
|---|---|
| 填地址 + 账号接入、探活（`/-api/v1/me`） | ✅ |
| 照片时间线（按月分组、缩略图、分页、原图查看、左右翻页） | ✅ |
| 影视列表 + 播放（Range 拖动、不可直放格式给提示） | ✅ |
| 文件浏览（只读） | ✅ |
| 笔记 / 账本 / 监控 | 入口占位，后续版本 |
| 扫码配对、设备令牌 | 待 P1-b（服务端要先上 token） |
| 相册自动备份 | 待 P2（要写权限 + 后台任务） |

## 对应的服务端接口

全部走 `/-api/v1/*`（见 `app/backend/duihao/webdavd/apiv1`），认证用 HTTP Basic，
与 WebDAV 客户端同一套账号：

```
GET /-api/v1/me              探活 + 账号 + 版本 + 能力
GET /-api/v1/fs/list         目录列表（cursor 分页）
GET /-api/v1/thumb           缩略图（JPEG，带 ETag）
GET /-api/v1/photos          照片时间线（按月分组）
GET /-api/v1/videos          视频列表
GET /-api/v1/videos/play     播放信息（mime / 可直放 / 播放地址）
```

## 跑起来

```bash
# 1) 装 Flutter（3.35+ 均可；国内可设镜像）
export PUB_HOSTED_URL=https://pub.flutter-io.cn
export FLUTTER_STORAGE_BASE_URL=https://storage.flutter-io.cn

# 2) 生成 android/ ios/ 平台目录（本仓库只提交 lib/ 与 pubspec.yaml）
cd app/mobile
flutter create --platforms=android,ios --org com.duihao --project-name duihao_mobile .

# 3) 依赖 + 静态检查
flutter pub get
flutter analyze

# 4) 连真机跑
flutter devices
flutter run -d <device-id>
```

打 Android 包需要本机装 Android SDK（`ANDROID_HOME`）+ JDK 17：

```bash
flutter build apk --release                    # 通用包（全 ABI，约 51 MB）
flutter build apk --release --split-per-abi    # 分 ABI，arm64 约 18 MB
```

## 发布签名

正式包用 `android/key.properties` + `android/app/duihao-release.jks`（两者都在
`.gitignore` 里，不入库）：

```properties
storePassword=<密码>
keyPassword=<密码>
keyAlias=duihao
storeFile=duihao-release.jks
```

> 这个 keystore 决定了「能不能覆盖升级已发布的 App」。丢了它，同一个包名就再也
> 装不上新版本（只能换包名重发）。请离线备份两份，密码单独存。

证书指纹（`apksigner verify --print-certs`）：

```
DN: CN=Duihao, OU=Duihao Mobile, O=Duihao, L=Beijing, ST=Beijing, C=CN
SHA-256: f0cbe1c0b1bfe2859ade74cf60ab63ebeddaf8dcad08e719050356b2c2250e77
SHA-1:   546777b36ccbfce4a29358f0e4aed45c4316365f
```

### 构建踩坑

- **`Could not close incremental caches`**（Kotlin 增量编译在本机失败）：
  `android/gradle.properties` 里已加 `kotlin.incremental=false`。
- **分 ABI 的 `armeabi-v7a` 包可能残缺**（AGP 9.1 已知现象：APK 里只剩 lib/，
  没有 `AndroidManifest.xml` / `resources.arsc`，装上会失败）。构建后务必检查：
  ```bash
  python -c "import zipfile;n=zipfile.ZipFile('X.apk').namelist();print('AndroidManifest.xml' in n)"
  ```
  出现残缺就 `flutter clean` 重建，或改用通用包（`app-release.apk`）。
- 国内镜像：Gradle 分发走 `mirrors.cloud.tencent.com`，Maven 走
  `maven.aliyun.com`（见 `android/settings.gradle.kts`），引擎产物走
  `storage.flutter-io.cn`（`FLUTTER_STORAGE_BASE_URL`）。

## 发版产物与下载地址

| 文件 | 说明 | 地址 |
|---|---|---|
| `duihao-android-arm64-v1.0.0.apk` | 主流手机（arm64，18 MB） | `https://duihao.com/download/` |
| `duihao-android-universal-v1.0.0.apk` | 兼容老机（全 ABI，51 MB） | 同上 |

校验值在同目录 `checksums.txt`。

## 已知限制（写在前面，免得白试）

- **HEIC 显示不了**：服务端没有纯 Go 的 HEIC 解码器，缩略图接口回 415，
  客户端显示「HEIC」占位。正解是桌面端上传时转 JPEG，或后续接 ffmpeg。
- **MKV 能不能放看编码**：走系统解码器，HEVC/AV1/TrueHD 可能黑屏，会提示去桌面端转码。
- **只读**：P1 没有上传/删除，避免在没权限模型时给用户「点了没反应」的错觉。
- **Basic 凭据存本地**：明文存 SharedPreferences，仅在设备被 root 时有风险；
  P1-b 换成设备令牌后，这里只留 token。
