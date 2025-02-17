#!/bin/bash

# Constants
readonly GITHUB_BASE_URL="https://github.com/nohara-cloud/nboard-node"
readonly GITHUB_API_URL="https://api.github.com/repos/nohara-cloud/nboard-node/releases/latest"
readonly GITHUB_DOWNLOAD_URL="${GITHUB_BASE_URL}/releases/download"
readonly SERVICE_FILE_URL="${GITHUB_BASE_URL}/raw/master/release/nboard-node.service"
readonly nbnode_SCRIPT_URL="${GITHUB_BASE_URL}/raw/master/release/nbnode.sh"

# Configuration paths
readonly INSTALL_DIR="/etc/nboard-node"
readonly BIN_PATH="/usr/local/bin/nboard-node"
readonly SERVICE_PATH="/etc/systemd/system/nboard-node.service"
readonly CONFIG_FILES=(
    "config.yml"
    "dns.json"
    "route.json"
    "custom_outbound.json"
    "custom_inbound.json"
    "rulelist"
    "geoip.dat"
    "geosite.dat"
)

# Color definitions
declare -A colors=(
    ["red"]='\033[0;31m'
    ["green"]='\033[0;32m'
    ["yellow"]='\033[0;33m'
    ["plain"]='\033[0m'
)

# Logging functions
log_info() {
    echo -e "${colors[green]}[INFO]${colors[plain]} $1"
}

log_warn() {
    echo -e "${colors[yellow]}[WARN]${colors[plain]} $1"
}

log_error() {
    echo -e "${colors[red]}[ERROR]${colors[plain]} $1"
}

# System check functions
check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "必须使用root用户运行此脚本！"
        exit 1
    fi
}

detect_architecture() {
    local detected_arch=$(arch)
    case "$detected_arch" in
        x86_64|x64|amd64)
            echo "64"
            ;;
        aarch64|arm64)
            echo "arm64-v8a"
            ;;
        s390x)
            echo "s390x"
            ;;
        *)
            log_warn "检测架构失败，使用默认架构: 64"
            echo "64"
            ;;
    esac
}

detect_os() {
    if [[ -f /etc/os-release ]]; then
        . /etc/os-release
        OS=$ID
        VERSION_ID=$VERSION_ID
    elif [[ -f /etc/lsb-release ]]; then
        . /etc/lsb-release
        OS=$DISTRIB_ID
        VERSION_ID=$DISTRIB_RELEASE
    else
        OS=$(grep -Eo 'centos|debian|ubuntu' /etc/issue 2>/dev/null || grep -Eo 'centos|debian|ubuntu' /proc/version 2>/dev/null || echo "unknown")
        VERSION_ID=$(grep -Eo '[0-9]+' /etc/issue 2>/dev/null || echo "0")
    fi

    OS=$(echo "$OS" | tr '[:upper:]' '[:lower:]')
    
    # Validate OS version
    case "$OS" in
        centos)
            if [[ $(echo "$VERSION_ID" | cut -d. -f1) -le 6 ]]; then
                log_error "请使用 CentOS 7 或更高版本的系统！" && exit 1
            fi
            ;;
        ubuntu)
            if [[ $(echo "$VERSION_ID" | cut -d. -f1) -lt 16 ]]; then
                log_error "请使用 Ubuntu 16 或更高版本的系统！" && exit 1
            fi
            ;;
        debian)
            if [[ $(echo "$VERSION_ID" | cut -d. -f1) -lt 8 ]]; then
                log_error "请使用 Debian 8 或更高版本的系统！" && exit 1
            fi
            ;;
        *)
            log_error "未检测到支持的系统版本！" && exit 1
            ;;
    esac
    
    echo "$OS"
}

# Installation functions
install_dependencies() {
    local os=$1
    log_info "安装依赖包..."
    
    if [[ "$os" == "centos" ]]; then
        yum install epel-release -y
        yum install wget curl unzip tar crontabs socat -y
    else
        apt update -y
        apt install wget curl unzip tar cron socat -y
    fi
}

get_latest_version() {
    local version=$1
    if [[ -z "$version" ]]; then
        curl -Ls "$GITHUB_API_URL" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/'
    else
        echo "${version#v}"
    fi
}

download_and_install() {
    local version=$1
    local arch=$2
    local download_url="${GITHUB_DOWNLOAD_URL}/${version}/nboard-node-linux-${arch}.zip"
    
    log_info "下载 Nboard Node ${version}..."
    wget -q -N --no-check-certificate -O "${INSTALL_DIR}/nboard-node-linux.zip" "$download_url"
    
    if [[ $? -ne 0 ]]; then
        log_error "下载失败，请检查网络连接和版本号！"
        exit 1
    fi
    
    # Extract and install
    unzip -q "${INSTALL_DIR}/nboard-node-linux.zip" -d /tmp/nboard-node
    chmod +x /tmp/nboard-node/nboard-node
    mv /tmp/nboard-node/nboard-node "$BIN_PATH"
    
    # Install service file
    wget -q -N --no-check-certificate -O "$SERVICE_PATH" "$SERVICE_FILE_URL"
    systemctl daemon-reload
    
    # Copy configuration files
    for file in "${CONFIG_FILES[@]}"; do
        if [[ ! -f "${INSTALL_DIR}/${file}" ]]; then
            cp "/tmp/nboard-node/${file}" "${INSTALL_DIR}/" 2>/dev/null || true
        fi
    done
    
    # Cleanup
    rm -f "${INSTALL_DIR}/nboard-node-linux.zip"
    rm -rf /tmp/nboard-node
}

setup_service() {
    systemctl stop nboard-node 2>/dev/null
    systemctl enable nboard-node
    systemctl start nboard-node
    
    sleep 2
    if systemctl is-active nboard-node >/dev/null 2>&1; then
        log_info "Nboard Node 服务启动成功！"
    else
        log_warn "Nboard Node 服务可能启动失败，请检查日志"
    fi
}

install_management_script() {
    curl -o /usr/bin/nbnode -Ls "$nbnode_SCRIPT_URL"
    chmod +x /usr/bin/nbnode
}

print_usage() {
    echo "Nboard Node 管理脚本使用方法: "
    echo "------------------------------------------"
    echo "nbnode                    - 显示管理菜单 (功能更多)"
    echo "nbnode start              - 启动 nboard-node"
    echo "nbnode stop               - 停止 nboard-node"
    echo "nbnode restart            - 重启 nboard-node"
    echo "nbnode status             - 查看 nboard-node 状态"
    echo "nbnode enable             - 设置 nboard-node 开机自启"
    echo "nbnode disable            - 取消 nboard-node 开机自启"
    echo "nbnode log                - 查看 nboard-node 日志"
    echo "nbnode update             - 更新 nboard-node"
    echo "nbnode update x.x.x       - 更新 nboard-node 指定版本"
    echo "nbnode config             - 显示配置文件内容"
    echo "nbnode install            - 安装 nboard-node"
    echo "nbnode uninstall          - 卸载 nboard-node"
    echo "nbnode version            - 查看 nboard-node 版本"
    echo "------------------------------------------"
}

# Main installation process
main() {
    local version=$1
    
    log_info "开始安装 Nboard Node..."
    
    # System checks
    check_root
    local os=$(detect_os)
    local arch=$(detect_architecture)
    
    # Create installation directory
    mkdir -p "$INSTALL_DIR"
    
    # Install dependencies
    install_dependencies "$os"
    
    # Get version and install
    version=$(get_latest_version "$version")
    download_and_install "$version" "$arch"
    
    # Setup service and management script
    setup_service
    install_management_script
    
    log_info "安装完成！"
    print_usage
}

# Start installation
main "$1"