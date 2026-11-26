#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "${ROOT_DIR}"

search_repository() {
  local pattern="$1"
  local case_mode="${2:-}"

  if command -v rg >/dev/null 2>&1; then
    # Search hidden paths such as .github too, matching the find fallback.
    local rg_flags=(-n --hidden)
    if [[ "${case_mode}" == "ignore-case" ]]; then
      rg_flags+=(-i)
    fi
    rg "${rg_flags[@]}" "${pattern}" \
      --glob '!.git/**' \
      --glob '!.claude/**' \
      --glob '!dist/**' \
      --glob '!test-results/**' \
      --glob '!tools/**' \
      --glob '!**/.terraform/**' \
      --glob '!private/**' \
      .
    return
  fi

  local grep_flags=(-nIE)
  if [[ "${case_mode}" == "ignore-case" ]]; then
    grep_flags=(-nIEi)
  fi

  find . \
    \( \
      -path './.git' -o \
      -path './.claude' -o \
      -path './dist' -o \
      -path './test-results' -o \
      -path './tools' -o \
      -path './private' -o \
      -path '*/.terraform' \
    \) -prune -o \
    -type f -print0 | xargs -0 grep "${grep_flags[@]}" "${pattern}" || true
}

check_no_search_matches() {
  local description="$1"
  local pattern="$2"
  local case_mode="${3:-}"

  local output
  set +e
  output="$(search_repository "${pattern}" "${case_mode}" 2>&1)"
  local status=$?
  set -e

  if [[ "${status}" -eq 0 && -n "${output}" ]]; then
    echo "${description}" >&2
    printf '%s\n' "${output}" >&2
    exit 1
  fi
  if [[ "${status}" -gt 1 ]]; then
    printf '%s\n' "${output}" >&2
    exit "${status}"
  fi
}

check_no_search_matches \
  "JWT-like token found outside ignored generated/private paths:" \
  'eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+'

check_no_search_matches \
  "Em dash found in repository files (use hyphens, colons, or parentheses instead):" \
  $'\xe2\x80\x94'

# Competitor comparisons and source links belong in documentation. Guard the
# implementation and dependencies against copied provider scaffolding instead.
set +e
legacy_references="$(grep -RniE '[s]nowflake|[s]nowflakedb' internal main.go go.mod go.sum)"
legacy_status=$?
set -e
if [[ "${legacy_status}" -eq 0 ]]; then
  echo "Unexpected legacy provider reference in implementation or dependencies:" >&2
  printf '%s\n' "${legacy_references}" >&2
  exit 1
elif [[ "${legacy_status}" -gt 1 ]]; then
  exit "${legacy_status}"
fi

if grep -RnE 't[.](Skip|Skipf)[(]' internal --include='*_test.go'; then
  echo "Go tests must use explicit build tags and hard preconditions instead of reporting skipped gates." >&2
  exit 1
fi

while IFS= read -r script_path; do
  [[ "${script_path}" == *"*"* ]] && continue
  script_path="${script_path#./}"
  if [[ ! -f "${script_path}" ]]; then
    echo "Makefile references missing script: ${script_path}" >&2
    exit 1
  fi
done < <(
  perl -ne 'while (m{(?:\./)?scripts/[A-Za-z0-9_./*-]+[.]sh}g) { print "$&\n" }' Makefile | sort -u
)

find README.md docs examples -type f -name '*.md' -print | sort | perl -e '
  use strict;
  use warnings;
  use Cwd qw(abs_path);
  use File::Spec;

  chomp(my @files = <STDIN>);
  my %anchors;

  sub index_anchors {
    my ($file, $key) = @_;
    $anchors{$key} = {};
    open my $fh, "<:encoding(UTF-8)", $file or die "$file: $!";
    my (%seen, $in_fence);
    while (my $line = <$fh>) {
      $in_fence = !$in_fence if $line =~ /^\s*(?:```|~~~)/;
      next if $in_fence;
      while ($line =~ /<a\s+[^>]*\b(?:id|name)="([^"]+)"/g) {
        $anchors{$key}{$1} = 1;
      }
      if ($line =~ /^#{1,6}\s+(.+?)\s*#*\s*$/) {
        my $h = lc($1);
        $h =~ s/\[([^\]]+)\]\([^)]+\)/$1/g;
        $h =~ s/`//g;
        $h =~ s/[^\p{L}\p{N}\s_-]//g;
        $h =~ s/ /-/g;
        next if $h eq "";
        my $n = $seen{$h}++;
        $anchors{$key}{$n ? "$h-$n" : $h} = 1;
      }
    }
    close $fh;
  }

  for my $f (@files) {
    my $self_key = abs_path($f) // $f;
    index_anchors($f, $self_key) unless exists $anchors{$self_key};

    open my $fh, "<:encoding(UTF-8)", $f or die "$f: $!";
    my ($dir) = $f =~ m{^(.*)/[^/]+$};
    $dir = "." unless defined $dir;
    my $in_fence = 0;
    while (my $line = <$fh>) {
      $in_fence = !$in_fence if $line =~ /^\s*(?:```|~~~)/;
      next if $in_fence;
      while ($line =~ /\[[^\]]+\]\(([^)]+)\)/g) {
        my $t = $1;
        next if $t eq "" || $t =~ m{^(?:https?://|mailto:)};
        $t =~ s/^<//;
        $t =~ s/>$//;
        my ($path, $frag) = split /#/, $t, 2;
        my $target_file = File::Spec->canonpath($path eq "" ? $f : "$dir/$path");
        if (!-e $target_file) {
          print STDERR "Broken local Markdown link in $f:$.: $t\n";
          exit 1;
        }
        if (defined $frag && $frag ne "" && -f $target_file && $target_file =~ /\.md$/) {
          my $target_key = abs_path($target_file) // $target_file;
          index_anchors($target_file, $target_key) unless exists $anchors{$target_key};
          if (!$anchors{$target_key}{$frag}) {
            print STDERR "Broken Markdown heading anchor in $f:$.: $t\n";
            exit 1;
          }
        }
      }
    }
    close $fh;
  }
'

while IFS= read -r script_path; do
  if perl -0ne 'if (/cleanup\n\s*trap - EXIT/) { $found = 1 } END { exit !$found }' "${script_path}"; then
    echo "Unsafe cleanup ordering in ${script_path}: disable the EXIT trap before explicit cleanup so a cleanup failure does not run twice." >&2
    exit 1
  fi
done < <(find scripts -maxdepth 1 -type f -name 'test-live-*.sh' -print | sort)
