#!/bin/bash
set -euo pipefail

# Debugging output
# set -x

show_help() {
	echo "Funnel Installation Script"
	echo
	echo "Usage:"
	echo "  $0 [version] [install_path]  # Install Funnel (default: latest version to \$HOME/.local/bin)"
	echo "  $0 --list                    # List available versions"
	echo "  $0 --help                    # Show this help"
}

list_tags() {
	RELEASES_URL="https://api.github.com/repos/ohsu-comp-bio/funnel/releases"

	# Get all releases and extract tag names
	RELEASES_JSON=$(curl -s "$RELEASES_URL")

	echo "$RELEASES_JSON" | grep '"tag_name":' | cut -d '"' -f 4 | head -20 | while read -r tag; do
		echo "$tag"
	done
}

# Global variables
# TODO: Move to respective functions?
VERSION=""
RELEASE_URL=""
ASSETS=""
OS=""
ARCH=""
TAR_FILE=""
CHECKSUM_FILE=""
DEST=""

# Arguments
while [[ $# -gt 0 ]]; do
	case $1 in
	--list | -l)
		list_tags
		exit 0
		;;
	--help | -h)
		show_help
		exit 0
		;;
	--version | -v)
		VERSION="$2"
		shift
		shift
		;;
	--dest | -d)
		# Deprecated flag
		DEST="$2"
		shift
		shift
		;;
	-*)
        echo "Unknown option: $1"
        show_help
        exit 1
        ;;
    *)
        # Handle positional arguments
        if [ -z "$VERSION" ]; then
            VERSION="$1"
        elif [ -z "$DEST" ]; then
            DEST="$1"
        else
            echo "Too many arguments: $1"
            show_help
            exit 1
        fi
        shift
        ;;
	esac
done

get_release_url() {
	if [ -z "$VERSION" ]; then
		echo "No version specified. Fetching the latest release..."
		RELEASE_URL="https://api.github.com/repos/ohsu-comp-bio/funnel/releases/latest"
		VERSION=$(curl -s $RELEASE_URL | grep '"tag_name":' | cut -d '"' -f 4)
	else
		echo "Fetching release for version $VERSION..."
		RELEASE_URL="https://api.github.com/repos/ohsu-comp-bio/funnel/releases/tags/$VERSION"
	fi
}

get_os_arch() {
	# OS/Arch
	OS=$(uname -s | tr '[:upper:]' '[:lower:]')
	ARCH=$(uname -m)

	if [ "$ARCH" == "x86_64" ]; then
		ARCH="amd64"
	elif [[ "$ARCH" == "aarch64" || "$ARCH" == "arm64" ]]; then
		ARCH="arm64"
	else
		echo "Unsupported architecture: $ARCH"
		exit 1
	fi
}

get_assets() {
	# Fetch the release assets URLs
	RELEASE_JSON=$(curl -s $RELEASE_URL)

	if echo "$RELEASE_JSON" | grep -q '"status": "404"'; then
		echo "Release $VERSION not found."
		echo
		echo "Available versions:"
		list_tags
		exit 1
	fi

	ASSETS=$(echo "$RELEASE_JSON" | grep "browser_download_url" | cut -d '"' -f 4)

	if [ -z "$ASSETS" ]; then
		echo "No assets found for release $VERSION. Exiting..."
		exit 1
	fi
}

download() {
	TAR_FILE="funnel-${OS}-${ARCH}"

	# Download the tar.gz file and checksums.txt for the detected OS and Arch
	echo "Downloading Funnel $VERSION for $OS $ARCH..."
	for asset in $ASSETS; do
		asset_name=$(basename "$asset")

		# Binary (tar.gz)
		if [[ "$asset" == *"${TAR_FILE}"* && "$asset" == *.tar.gz ]]; then
			TAR_URL="$asset"
			TAR_NAME="$asset_name"
			echo " ➜ $TAR_NAME"
			curl -L --progress-bar -o "$TAR_NAME" "$TAR_URL"

		# Checksums
		elif [[ "$asset_name" == *checksums* ]]; then
			CHECKSUM_FILE="$asset_name"
			echo " ➜ $CHECKSUM_FILE"
			curl -L --progress-bar -o "$CHECKSUM_FILE" "$asset"
		fi
	done
}

verify() {
	# Verify checksum
	echo "Verifying checksum..."
	CHECKSUM_EXPECTED=$(grep $TAR_NAME $CHECKSUM_FILE | awk '{print $1}')
	CHECKSUM_ACTUAL=$(shasum -a 256 $TAR_NAME | awk '{print $1}')

	if [ "$CHECKSUM_EXPECTED" != "$CHECKSUM_ACTUAL" ]; then
		echo "Checksum verification failed for $TAR_NAME. Exiting..."
		exit 1
	fi
}

install() {
	# Extract and install the package
	echo "Extracting the package..."
	tar -xzf $TAR_NAME

	# Determine where to install the Funnel binary
	if [ -z "$DEST" ]; then
		DEST=$HOME/.local/bin
	fi
	echo "Installing Funnel to $DEST..."
	mkdir -p $DEST
	mv funnel $DEST

	# Clean up
	rm $TAR_NAME $CHECKSUM_FILE

	echo "Installation successful: $DEST/funnel"
	echo
	$DEST/funnel version
	echo
	echo "Run '$DEST/funnel --help' for more info"
}

main() {
	get_release_url
	get_os_arch
	get_assets
	download
	verify
	install
}

main
