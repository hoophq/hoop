#!/bin/bash

# This script assumes you have access to the socket
# 127.0.0.1:3001

if [ -z "$1" ]
  then
    echo "Please provide the org name as argument"
    exit 1
fi

ORG=$1

echo "Do you want to erase all data from org '$ORG'? [y/n]"
while read line; do
  STDIN="$line"
  break
done

if [ "$STDIN" != "y" ]; then
    echo "Aborting..."
    exit 0
fi

echo "moving forward"