#!/usr/bin/env bash
#Got this from https://www.digitalocean.com/community/tutorials/how-to-build-go-executables-for-multiple-platforms-on-ubuntu-16-04

package=$1
if [[ -z "$package" ]]; then
  echo "usage: $0 <package-name>"
  exit 1
fi
package_split=(${package//\// })
#package_name=${package_split[-1]}
package_name="landscaper"	
platforms=("windows/amd64" "darwin/amd64" "darwin/arm64" "linux/amd64" "linux/arm64")
echo 'Starting build...'
for platform in "${platforms[@]}"
do
	platform_split=(${platform//\// })
	GOOS=${platform_split[0]}
	GOARCH=${platform_split[1]}
	output_name="build/"$package_name'-'$GOOS'-'$GOARCH
	if [ $GOOS = "windows" ]; then
		output_name+='.exe'
	fi	

	env GOOS=$GOOS GOARCH=$GOARCH go build -o $output_name $package
	if [ $? -ne 0 ]; then
   		echo 'An error has occurred! Aborting the script execution...'
		exit 1
	fi
    echo 'Completed: '$output_name
done
echo 'Compress build...'
zip -r build/landscaper.zip build

echo 'Clean files...'
rm build/landscaper-windows-amd64.exe
rm build/landscaper-darwin-amd64
rm build/landscaper-darwin-arm64
rm build/landscaper-linux-amd64
rm build/landscaper-linux-arm64

echo 'All tasks completed successfully'