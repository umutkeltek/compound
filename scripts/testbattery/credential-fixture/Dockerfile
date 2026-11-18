# syntax=docker/dockerfile:1.12.0@sha256:6b5e0df6164e05eee9147432326ab6ef98f7b4ce7a3ef2a2977ce86c62dc5d39
# Rebuild on linux/amd64. Every input is pinned to the bytes used by the native fixture.
ARG SOURCE_DATE_EPOCH=0
FROM docker.io/library/docker@sha256:cd58b396e427d1ee8cddcc3f3b7e8d8c2ba45755c5dd5b821b6e615e1ccf4586 AS cli
FROM docker.io/library/postgres@sha256:996e9dc583ba9f0265a2c75547a9c95fb22c251e394749cab205a0a00e1856a1 AS postgres

FROM postgres AS packages
# Binary dependencies selected by Debian's signed trixie indexes for Python 3.13 and sudo.
ADD --checksum=sha256:4158976c77038b93021967f425b4cc505df18c4f5e3f444732399e7ed4af4419 https://snapshot.debian.org/file/1b97eac07c01f27647376cf8adbce30a0879b7da /packages/libapparmor1.deb
ADD --checksum=sha256:feff874772cae904ae4d24e917b35664952bec4b4ae9100a4edda5a7fa746832 https://snapshot.debian.org/file/074f4df385e654b2b9aaeda0eeb8be65608b7e22 /packages/libexpat1.deb
ADD --checksum=sha256:7e142ed64a81ccdd39714f213467bd365736ef341adb2be11be981f638b9175f https://snapshot.debian.org/file/ee2d3c4d8cec0f62aff45a3414d1d4eda120b3c4 /packages/libpython3-stdlib.deb
ADD --checksum=sha256:a4aa6a9a8c77f87bfe545e1d14ec42c001d189ace90e3aa75deec882b39dee49 https://snapshot.debian.org/file/f79e62140e3c4334d077864ff57ca16f0ccd2823 /packages/libpython3.13-minimal.deb
ADD --checksum=sha256:db161322a3481d2c0c3b9a3b9a03c3ab0e2b1718f54b88755fb4a3f939165b84 https://snapshot.debian.org/file/c8d9cc73aab111fac7dc1b26c6e2ffda7af08f04 /packages/libpython3.13-stdlib.deb
ADD --checksum=sha256:0788e4b0b72db1bcf74a19cb24c3511e7770ad0b341f0f30e361701c3072f53e https://snapshot.debian.org/file/ede2c5c9f1526d5a0f1d05b438bf9cdc942d2711 /packages/media-types.deb
ADD --checksum=sha256:c1388174dc7140e6c19a6dd39f8a0dcb38a835cd66c65644e0dded43d5223cb0 https://snapshot.debian.org/file/c4bc1927409f9fb90398eff167fe0344eaacbf17 /packages/python3.deb
ADD --checksum=sha256:a5d2371bebf3689f676e3f3bae800371bcc4392a8a51269e23d528249b399615 https://snapshot.debian.org/file/42c052a6da47f6af554a65b5dd9f80e1da07ef1e /packages/python3-minimal.deb
ADD --checksum=sha256:460c1fd72845516ac3ce9d1686916f3ab2e4c86f8a9746e14d26243a47e2b98d https://snapshot.debian.org/file/ca853a3b72a03169307b975ee16fe3ca22cb0754 /packages/python3.13.deb
ADD --checksum=sha256:c1fda9e7b1bf15b2fa75a238d8a8710e142fe14c722fc650c5fc614f078c9073 https://snapshot.debian.org/file/0f40ed62682a75e831ace51a76884e8b531688ad /packages/python3.13-minimal.deb
ADD --checksum=sha256:4f926f8f0704575158a90135713977f98f7d50d185969d9a8a405cdce5e3e50c https://snapshot.debian.org/file/578aedf738e7a243381a1c33871c48a5a1ca3136 /packages/sudo.deb
COPY --from=cli /usr/local/bin/docker /overlay/usr/bin/docker
RUN --network=none for package in /packages/*.deb; do dpkg-deb -x "$package" /overlay || exit; done && \
    find /overlay -exec touch -h --date=@0 {} +

FROM postgres
COPY --from=packages /overlay/ /
