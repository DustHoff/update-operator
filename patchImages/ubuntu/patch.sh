#!/bin/bash
echo run as
id
echo deleting existing sources
rm /host/etc/apt/sources.list.d/*.list
rm /host/etc/apt/sources.list

echo update apt repositories
cp /patch/sources/ubuntu.list /host/etc/apt/sources.list
cp /patch/sources/crio.list   /host/etc/apt/sources.list.d/crio.list
cp -r /patch/keyrings         /host/usr/share/keyrings

chroot /host apt-get update


