# Tool to parse series of digital images in RAW format and build a family of charachteristic curves

## Example usage

Parse images and save data for later use (and build curves PNG)

    go run main.go --scan-dir <directory with images> --save-data-to expodata.json --data-overwrite -v

Save curves to points.png in current directory using JSON data from previews step

    go run main.go -v --read-data-from expodata.json
