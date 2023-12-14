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
apt-get -o Dir=/host update

if [ -n "${HOLDPKG}" ]
then
  echo hold specified packages: ${HOLDPKG}
  chroot /host su - root -l -c "apt-mark hold ${HOLDPKG}"
fi

if [ -n "${INSTALLPKG}" ]
then
  echo force install specified packages: ${INSTALLPKG}
  apt-get -o Dir=/host install -y ${INSTALLPKG}
fi

echo upgrade all packages
apt-get -o Dir=/host upgrade -y

echo reboot system in 10min
chroot /host su - root -l -c "sudo shutdown -r +10"


