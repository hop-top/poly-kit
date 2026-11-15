<?php

declare(strict_types=1);

namespace HopTop\KitEngine;

/**
 * List parameters; unset fields are omitted from the query string.
 *
 * `sort` accepts id, created_at, updated_at (ascending only); `search` is
 * a substring match over the serialized document.
 */
final class CollectionQuery
{
    public function __construct(
        public readonly ?int $limit = null,
        public readonly ?int $offset = null,
        public readonly ?string $sort = null,
        public readonly ?string $search = null,
    ) {
    }

    /** @return array<string,int|string> */
    public function toQuery(): array
    {
        $q = [];
        if ($this->limit !== null) {
            $q['limit'] = $this->limit;
        }
        if ($this->offset !== null) {
            $q['offset'] = $this->offset;
        }
        if ($this->sort !== null && $this->sort !== '') {
            $q['sort'] = $this->sort;
        }
        if ($this->search !== null && $this->search !== '') {
            $q['search'] = $this->search;
        }

        return $q;
    }
}
