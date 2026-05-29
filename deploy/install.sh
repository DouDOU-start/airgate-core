#!/bin/bash
# ============================================================
# AirGate Core - 裸金属安装脚本（systemd）
#
# 用法：
#   curl -sSL https://raw.githubusercontent.com/DevilGenius/airgate-core/master/deploy/install.sh | sudo bash
#
# 或带子命令：
#   sudo bash install.sh install    # 安装最新版本（默认动作）
#   sudo bash install.sh upgrade    # 升级到最新版本
#   sudo bash install.sh uninstall  # 卸载（保留 /etc/airgate-core 与 /var/lib/airgate-core）
#   sudo bash install.sh -v v0.x.y  # 安装指定版本
#
# 前置条件：
#   - Linux x86_64 / arm64
#   - 已运行的 PostgreSQL 15+
#   - 已运行的 Redis 7+
#   - root 权限（systemd 需要）
#
# 这个脚本会做：
#   1. 从 GitHub Releases 下载对应平台的 airgate-core 二进制（前端 SPA 已内嵌）
#   2. 安装到 /opt/airgate-core/airgate-core
#   3. 创建系统用户 airgate
#   4. 创建 /etc/airgate-core / /var/lib/airgate-core 目录
#   5. 安装 systemd 服务 airgate-core.service
#
# 它故意不替你启动服务、不写 config.yaml —— 装完之后请：
#   sudo systemctl start airgate-core
#   sudo systemctl enable airgate-core
#   浏览器打开 http://<your-host>:9517 走安装向导
#
# 安装向导会引导你输入 PostgreSQL / Redis 连接信息和管理员账号，
# 写入 /etc/airgate-core/config.yaml。
#
# 想要自带 PostgreSQL + Redis 的 Docker Compose 部署？请改用 deploy/docker-deploy.sh。
# ============================================================

set -e

# ---- Constants ----
GITHUB_REPO="${GITHUB_REPO:-DevilGenius/airgate-core}"
AIRGATE_BRANCH="${AIRGATE_BRANCH:-master}"
INSTALL_DIR="/opt/airgate-core"
CONFIG_DIR="/etc/airgate-core"
DATA_DIR="/var/lib/airgate-core"
SERVICE_NAME="airgate-core"
SERVICE_USER="airgate"

# Colors
#
# 使用 bash ANSI-C quoting `$'...'` 让变量直接保存真正的 ESC 字符（0x1B），
# 这样无论是 echo / printf / heredoc 都能正确显示颜色。
# 之前用 '\033[...]' 字面量在 heredoc 里会被原样输出乱码。
if [ -t 1 ]; then
    RED=$'\033[0;31m'
    GREEN=$'\033[0;32m'
    YELLOW=$'\033[1;33m'
    BLUE=$'\033[0;34m'
    CYAN=$'\033[0;36m'
    NC=$'\033[0m'
else
    RED=''
    GREEN=''
    YELLOW=''
    BLUE=''
    CYAN=''
    NC=''
fi

# ---- Logging ----
print_info()    { echo "${BLUE}[信息]${NC} $1"; }
print_success() { echo "${GREEN}[成功]${NC} $1"; }
print_warning() { echo "${YELLOW}[警告]${NC} $1"; }
print_error()   { echo "${RED}[错误]${NC} $1" >&2; }

# ---- Banner ----
print_banner() {
    cat <<'BANNER'

    ╔═══════════════════════════════════════════════╗
    ║          AirGate Core Installer               ║
    ║       Pluggable AI Gateway · Bare Metal       ║
    ╚═══════════════════════════════════════════════╝

BANNER
}

# ---- Check root ----
check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        print_error "请使用 root 权限运行（sudo bash install.sh）"
        exit 1
    fi
}

# ---- Detect OS / arch ----
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$ARCH" in
        x86_64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *)
            print_error "不支持的架构: $ARCH（仅支持 amd64 / arm64）"
            exit 1
            ;;
    esac

    case "$OS" in
        linux) OS="linux" ;;
        darwin)
            print_error "macOS 不支持 systemd，请使用 Docker Compose 部署：deploy/docker-deploy.sh"
            exit 1
            ;;
        *)
            print_error "不支持的操作系统: $OS"
            exit 1
            ;;
    esac

    print_info "检测到平台: ${OS}/${ARCH}"
}

# ---- Check dependencies ----
check_dependencies() {
    local missing=()
    command -v curl >/dev/null 2>&1 || missing+=("curl")
    command -v sha256sum >/dev/null 2>&1 || missing+=("sha256sum (coreutils)")
    command -v systemctl >/dev/null 2>&1 || missing+=("systemctl (systemd)")

    if [ ${#missing[@]} -gt 0 ]; then
        print_error "缺少依赖: ${missing[*]}"
        print_info "请先安装这些工具后再运行本脚本"
        exit 1
    fi
}

# ---- Get latest version ----
get_latest_version() {
    print_info "获取最新版本..."
    LATEST_VERSION=$(curl -fsSL --connect-timeout 10 --max-time 30 \
        "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" 2>/dev/null \
        | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')

    if [ -z "$LATEST_VERSION" ]; then
        print_error "获取最新版本失败，请检查网络"
        exit 1
    fi

    print_info "最新版本: $LATEST_VERSION"
}

# ---- Validate explicit version ----
validate_version() {
    local v="$1"
    [[ "$v" =~ ^v ]] || v="v$v"
    print_info "校验版本 $v ..."
    local code
    code=$(curl -fsSL -o /dev/null -w "%{http_code}" --connect-timeout 10 --max-time 30 \
        "https://api.github.com/repos/${GITHUB_REPO}/releases/tags/${v}" 2>/dev/null || echo "000")
    if [ "$code" != "200" ]; then
        print_error "版本 $v 不存在（GitHub 返回 $code）"
        exit 1
    fi
    LATEST_VERSION="$v"
    print_info "已选择版本: $LATEST_VERSION"
}

# ---- Get currently installed version ----
get_current_version() {
    if [ -f "$INSTALL_DIR/airgate-core" ]; then
        "$INSTALL_DIR/airgate-core" --version 2>/dev/null \
            | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo "unknown"
    else
        echo "not_installed"
    fi
}

# ---- Download and install binary ----
download_and_install_binary() {
    local asset="airgate-core-${OS}-${ARCH}"
    local url="https://github.com/${GITHUB_REPO}/releases/download/${LATEST_VERSION}/${asset}"
    local checksum_url="${url}.sha256"

    print_info "下载 ${asset} ..."
    TEMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TEMP_DIR"' EXIT

    if ! curl -fL --progress-bar "$url" -o "$TEMP_DIR/$asset"; then
        print_error "下载失败: $url"
        exit 1
    fi

    print_info "校验 SHA256 ..."
    if curl -fsSL "$checksum_url" -o "$TEMP_DIR/${asset}.sha256" 2>/dev/null; then
        local expected actual
        expected=$(awk '{print $1}' "$TEMP_DIR/${asset}.sha256")
        actual=$(sha256sum "$TEMP_DIR/$asset" | awk '{print $1}')
        if [ "$expected" != "$actual" ]; then
            print_error "校验失败"
            print_error "  期望: $expected"
            print_error "  实际: $actual"
            exit 1
        fi
        print_success "校验通过"
    else
        print_warning "未找到 .sha256 文件，跳过校验"
    fi

    mkdir -p "$INSTALL_DIR"
    install -m 0755 "$TEMP_DIR/$asset" "$INSTALL_DIR/airgate-core"
    print_success "二进制已安装到 $INSTALL_DIR/airgate-core"
}

# ---- Create system user ----
create_user() {
    if id "$SERVICE_USER" &>/dev/null; then
        print_info "系统用户 $SERVICE_USER 已存在"
    else
        print_info "创建系统用户 $SERVICE_USER ..."
        # /bin/sh 是为了兼容 systemd ExecStartPost 之类的脚本调用，
        # 不分配 home / 不允许登录（无密码）。
        useradd -r -s /bin/sh -d "$INSTALL_DIR" -c "AirGate Core service" "$SERVICE_USER"
        print_success "用户已创建"
    fi
}

# ---- Setup directories ----
setup_directories() {
    print_info "创建目录与权限 ..."
    mkdir -p "$INSTALL_DIR" "$CONFIG_DIR" "$DATA_DIR/plugins" "$DATA_DIR/uploads" "$DATA_DIR/assets"
    chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR" "$DATA_DIR"
    chown -R "$SERVICE_USER:$SERVICE_USER" "$CONFIG_DIR"
    chmod 750 "$CONFIG_DIR"
    print_success "目录就绪"
}

# ---- Install systemd unit ----
install_service() {
    local unit_url="https://raw.githubusercontent.com/${GITHUB_REPO}/${AIRGATE_BRANCH}/deploy/airgate-core.service"
    print_info "下载并安装 systemd unit ..."
    if ! curl -fsSL "$unit_url" -o "/etc/systemd/system/${SERVICE_NAME}.service"; then
        print_error "下载 systemd unit 失败"
        exit 1
    fi
    systemctl daemon-reload
    print_success "systemd 服务已安装到 /etc/systemd/system/${SERVICE_NAME}.service"
}

# ---- Detect public IP for completion message ----
detect_public_ip() {
    PUBLIC_IP=$(curl -fsSL --connect-timeout 5 --max-time 10 https://ipinfo.io/ip 2>/dev/null || true)
    [ -n "$PUBLIC_IP" ] || PUBLIC_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "<your-host>")
}

# ---- Print install completion ----
print_install_complete() {
    detect_public_ip
    cat <<DONE

=================================================
${GREEN}✓ AirGate Core ${LATEST_VERSION} 安装完成${NC}
=================================================

  二进制   : ${CYAN}${INSTALL_DIR}/airgate-core${NC}
  配置目录 : ${CYAN}${CONFIG_DIR}${NC}
  数据目录 : ${CYAN}${DATA_DIR}${NC}
  systemd  : ${CYAN}/etc/systemd/system/${SERVICE_NAME}.service${NC}

${BLUE}下一步${NC}

  1. 启动服务：
     ${CYAN}sudo systemctl start ${SERVICE_NAME}${NC}

  2. 设置开机自启：
     ${CYAN}sudo systemctl enable ${SERVICE_NAME}${NC}

  3. 浏览器打开安装向导：
     ${CYAN}http://${PUBLIC_IP}:9517${NC}

     向导会引导你完成：
       - PostgreSQL 连接配置
       - Redis 连接配置
       - 创建管理员账号

     最终的连接信息会写到 ${CONFIG_DIR}/config.yaml。

${BLUE}常用命令${NC}

  sudo systemctl status ${SERVICE_NAME}    # 查看状态
  sudo journalctl -u ${SERVICE_NAME} -f    # 查看日志
  sudo systemctl restart ${SERVICE_NAME}   # 重启
  sudo systemctl stop ${SERVICE_NAME}      # 停止

${BLUE}卸载${NC}

  curl -sSL https://raw.githubusercontent.com/${GITHUB_REPO}/${AIRGATE_BRANCH}/deploy/install.sh | sudo bash -s -- uninstall

DONE
}

# ---- Upgrade ----
upgrade() {
    if [ ! -f "$INSTALL_DIR/airgate-core" ]; then
        print_error "尚未安装，请先运行 install"
        exit 1
    fi

    local current
    current=$(get_current_version)
    print_info "当前版本: $current"

    if [ -z "${LATEST_VERSION:-}" ]; then
        get_latest_version
    fi

    if [ "$current" = "$LATEST_VERSION" ] || [ "$current" = "${LATEST_VERSION#v}" ]; then
        print_warning "已是最新版本 ($LATEST_VERSION)"
        return
    fi

    print_info "升级 $current → $LATEST_VERSION"

    if systemctl is-active --quiet "$SERVICE_NAME"; then
        print_info "停止服务 ..."
        systemctl stop "$SERVICE_NAME"
    fi

    cp "$INSTALL_DIR/airgate-core" "$INSTALL_DIR/airgate-core.backup.$(date +%Y%m%d%H%M%S)"
    print_info "已备份旧版本到 ${INSTALL_DIR}/airgate-core.backup.*"

    download_and_install_binary
    chown "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR/airgate-core"

    print_info "启动服务 ..."
    systemctl start "$SERVICE_NAME"

    print_success "升级完成: $(get_current_version)"
}

# ---- Uninstall ----
uninstall() {
    print_warning "即将卸载 AirGate Core："
    echo "  - 停止并禁用 systemd 服务"
    echo "  - 删除 ${INSTALL_DIR}（含二进制与备份）"
    echo "  - 删除 systemd unit 文件"
    echo ""
    echo "  ${YELLOW}保留${NC} ${CONFIG_DIR}（配置）"
    echo "  ${YELLOW}保留${NC} ${DATA_DIR}（数据 / 已安装的插件）"
    echo "  ${YELLOW}保留${NC} 系统用户 ${SERVICE_USER}"
    echo ""

    if [ "${ASSUME_YES:-0}" != "1" ]; then
        if [ -e /dev/tty ] && [ -r /dev/tty ]; then
            read -r -p "确认卸载？输入 'yes' 继续: " ans < /dev/tty || ans=""
            [ "$ans" = "yes" ] || { print_info "已取消"; exit 0; }
        else
            print_error "非交互模式下卸载需要 ASSUME_YES=1"
            exit 1
        fi
    fi

    if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
        systemctl stop "$SERVICE_NAME" || true
    fi
    if systemctl is-enabled --quiet "$SERVICE_NAME" 2>/dev/null; then
        systemctl disable "$SERVICE_NAME" || true
    fi

    rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
    systemctl daemon-reload
    rm -rf "$INSTALL_DIR"

    print_success "卸载完成。配置 (${CONFIG_DIR}) 与数据 (${DATA_DIR}) 已保留。"
    print_info "如需彻底清理：sudo rm -rf ${CONFIG_DIR} ${DATA_DIR} && sudo userdel ${SERVICE_USER}"
}

# ---- Install (default) ----
do_install() {
    if [ -f "$INSTALL_DIR/airgate-core" ]; then
        print_warning "检测到已安装：$(get_current_version)"
        print_info "如需升级请运行：sudo bash install.sh upgrade"
        print_info "如需卸载请运行：sudo bash install.sh uninstall"
        exit 0
    fi

    if [ -z "${LATEST_VERSION:-}" ]; then
        get_latest_version
    fi

    download_and_install_binary
    create_user
    setup_directories
    install_service
    chown "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR/airgate-core"
    print_install_complete
}

# ---- CLI parsing ----
main() {
    print_banner
    check_root
    detect_platform
    check_dependencies

    local action="install"
    while [ $# -gt 0 ]; do
        case "$1" in
            install|upgrade|uninstall)
                action="$1"; shift ;;
            -v|--version)
                shift
                [ $# -gt 0 ] || { print_error "--version 需要一个参数（如 v0.1.0）"; exit 1; }
                validate_version "$1"
                shift ;;
            -y|--yes)
                ASSUME_YES=1; shift ;;
            -h|--help)
                cat <<HELP
用法: sudo bash install.sh [action] [选项]

Actions:
  install     安装最新版本（默认）
  upgrade     升级到最新版本
  uninstall   卸载（保留配置与数据）

Options:
  -v, --version <ver>   指定版本（如 v0.1.0）
  -y, --yes             非交互确认（适用于 uninstall）
  -h, --help            显示帮助

示例:
  curl -sSL .../install.sh | sudo bash
  curl -sSL .../install.sh | sudo bash -s -- -v v0.1.0
  curl -sSL .../install.sh | sudo bash -s -- uninstall -y

HELP
                exit 0 ;;
            *)
                print_error "未知参数: $1"
                exit 1 ;;
        esac
    done

    case "$action" in
        install)   do_install ;;
        upgrade)   upgrade ;;
        uninstall) uninstall ;;
    esac
}

main "$@"
