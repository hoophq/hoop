#!/bin/bash
{
    set -e
    SUDO=''
    if [ "$(id -u)" != "0" ]; then
      SUDO='sudo'
      echo "This script requires superuser access."
      echo "You will be prompted for your password by sudo."
      # clear any previous sudo permission
      sudo -k
    fi


    # run inside sudo
    $SUDO bash <<SCRIPT
  set -e

  echoerr() { echo "\$@" 1>&2; }

  if [[ ! ":\$PATH:" == *":/usr/local/bin:"* ]]; then
    echoerr "Your path is missing /usr/local/bin, you need to add this to use this installer."
    exit 1
  fi

  if [ "\$(uname)" == "Darwin" ]; then
    OS=Darwin
  elif [ "\$(expr substr \$(uname -s) 1 5)" == "Linux" ]; then
    OS=Linux
  else
    echoerr "This installer is only supported on Linux and MacOS"
    exit 1
  fi

  ARCH="\$(uname -m)"
  if [ "\$ARCH" == "x86_64" ]; then
    ARCH=x86_64
  elif [[ "\$ARCH" == "amd64" ]]; then
    ARCH=x86_64
  elif [[ "\$ARCH" == "aarch64" ]]; then
    ARCH=arm64
  elif [[ "\$ARCH" == "arm64" ]]; then
    ARCH=arm64
  else
    echoerr "unsupported arch: \$ARCH"
    exit 1
  fi
  
  if [ \$(command -v curl) ]; then
    VERSION=\$(curl -s https://releases.hoop.dev/release/latest.txt)
    URL=https://releases.hoop.dev/release/\${VERSION}/hoop_\${VERSION}_\${OS}_\${ARCH}.tar.gz
    echo "Installing CLI from \$URL"
    curl -s -L "\$URL" -o hoop_\${VERSION}.tar.gz
  elif [ \$(command -v wget) ]; then
    VERSION=\$(wget -q -O - https://releases.hoop.dev/release/latest.txt)
    URL=https://releases.hoop.dev/release/\${VERSION}/hoop_\${VERSION}_\${OS}_\${ARCH}.tar.gz
    wget "\$URL" -O hoop_\${VERSION}.tar.gz
  else
    echoerr "curl or wget is required to install it"
    exit 1
  fi
  tar --extract --file hoop_\${VERSION}.tar.gz -C /usr/local/bin && rm -f hoop_\${VERSION}.tar.gz
SCRIPT
  # test the CLI
  LOCATION=/usr/local/bin/hoop
  echo "hoop installed to $LOCATION"
  /usr/local/bin/hoop version
}