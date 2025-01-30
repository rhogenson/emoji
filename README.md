# emoji list

A basic list of emojis and their annotations. Useful for building emoji pickers.

## Examples

Here's a minimal emoji picker using dmenu:

```
dmenu -i -l20 <emoji_list | cut -d' ' -f1 | tr -d '\n' | xclip -selection clipboard
```

Or the equivalent for wayland:

```
bemenu -i -l20 <emoji_list | cut -d' ' -f1 | tr -d '\n' | wl-copy
```

Or using fzf inside tmux:

```
fzf <emoji_list | cut -d' ' -f1 | tr -d '\n' | tmux load-buffer -w -
```

## Data source

Data is pulled from unicode.org using the [generate.go](generate/generate.go) script.
