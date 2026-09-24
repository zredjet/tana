package fsops

// fatFileSizeLimit は、FAT 系のファイルシステムのファイルの大きさの上限（§10.6。4 GiB − 1 バイト）。
const fatFileSizeLimit = 1<<32 - 1
