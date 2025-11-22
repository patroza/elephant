PREFIX=/usr
make build
make install

for D in internal/providers/*; do
    if [ -d "${D}" ]; then
        cd "${D}"   # your processing here
        make clean
        make build
        make install
        cd ../../..
    fi
done
