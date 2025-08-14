function parseDepArgs() {
    while [[ $# -gt 0 ]]; do
        case "${1}" in
        --version=*)
            VERSION="${1#*=}"
            shift
            ;;
        *)
            return 1
            ;;
        esac
    done
}

function printDepHelp() {
    echo -e "  ${COLOR_LIGHT_YELLOW}--version=<version>${COLOR_RESET}     - Set the build version (default: 'dev')"
}

function printDepEnvHelp() {
    echo -e "  ${COLOR_LIGHT_GREEN}VERSION${COLOR_RESET}      - Set the build version (default: 'dev')"
}

function initDep() {
    local git_commit
    git_commit="$(git rev-parse --short HEAD)" || git_commit="dev"
    setDefault "VERSION" "${git_commit}"

    # replace space, newline, and double quote
    VERSION="$(echo "$VERSION" | sed 's/ //g' | sed 's/"//g' | sed 's/\n//g')"
    echo -e "${COLOR_LIGHT_BLUE}Version:${COLOR_RESET} ${COLOR_LIGHT_CYAN}${VERSION}${COLOR_RESET}"
    if [[ "${VERSION}" != "dev" ]] && [[ "${VERSION}" != "${git_commit}" ]] && [[ ! "${VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-beta.*|-rc.*|-alpha.*)?$ ]]; then
        echo -e "${COLOR_LIGHT_RED}Version format error: ${VERSION}${COLOR_RESET}"
        return 1
    fi

    addLDFLAGS "-X 'github.com/PeterChen1997/synctv/internal/version.Version=${VERSION}'"
addLDFLAGS "-X 'github.com/PeterChen1997/synctv/internal/version.GitCommit=${git_commit}'"
    addTags "jsoniter"
}
