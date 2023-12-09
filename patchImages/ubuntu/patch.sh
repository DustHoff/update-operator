#!/bin/bash
echo run as
id
echo deleting existing sources
rm -f /host/etc/apt/sources.list.d/*.list
rm -f /host/etc/apt/sources.list

echo update apt repositories
cp /patch/source/ubuntu.list /host/etc/apt/sources.list
cp /patch/source/crio.list   /host/etc/apt/sources.list.d/crio.list
cp -r /patch/keyrings         /host/usr/share/keyrings

chroot /host bash -ic 'apt-get update'


