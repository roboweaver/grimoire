// Package php implements the minimal subset of PHP's serialize()/unserialize()
// format that grimoire needs for WordPress compatibility.
//
// WordPress stores several usermeta values as PHP-serialized data — most
// importantly the {prefix}capabilities role map (for example
// a:1:{s:13:"administrator";b:1;}). This package provides pure-Go encoding and
// decoding for the value shapes grimoire reads and writes: booleans, integers,
// strings, and associative string-keyed arrays. It deliberately does not model
// PHP objects, floats, or references, which grimoire never needs.
package php
