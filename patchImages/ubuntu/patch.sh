#!/bin/bash
set -e
echo run as
id
echo deleting existing sources
rm -f /host/etc/apt/sources.list.d/ubuntu.list
rm -f /host/etc/apt/sources.list.d/crio.list
rm -f /host/etc/apt/sources.list

echo update apt repositories
cp /patch/source/ubuntu.list /host/etc/apt/sources.list
cp /patch/source/crio.list   /host/etc/apt/sources.list.d/crio.list
cp -r /patch/keyrings         /host/usr/share/keyrings

echo fetch new updates from repositories
chroot /host su - root -l -c 'apt-get update'

if [ -n "${HOLDPKG}" ]
then
  echo hold specified packages: ${HOLDPKG}
  chroot /host su - root -l -c "apt-mark hold ${HOLDPKG}"
fi

if [ -n "${INSTALLPKG}" ]
then
  echo force install specified packages: ${INSTALLPKG}
  chroot /host su - root -l -c "DEBIAN_FRONTEND=noninteractive apt-get install -y ${INSTALLPKG}"
fi

echo remove old packages
chroot /host su - root -l -c "DEBIAN_FRONTEND=noninteractive apt-get autoremove -y"

echo upgrade all packages
chroot /host su - root -l -c "DEBIAN_FRONTEND=noninteractive apt-get upgrade -y"

echo reboot system in 10min
chroot /host su - root -l -c "sudo shutdown -r +5"


