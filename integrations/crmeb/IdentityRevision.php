<?php
declare(strict_types=1);
namespace OmniChat;

// CRMEB-specific identity adapter. No revision is accepted from request fields.
final class IdentityRevision
{
    const CLAIM = 'omni_identity_revision';

    public static function current(string $realm, int $id): string
    {
        if (!in_array($realm, ['api', 'kefu'], true) || $id <= 0) throw new \RuntimeException('identity_revision');
        $value = \think\facade\Db::name('omni_identity_revision')->where(['realm'=>$realm, 'subject_id'=>$id])->value('revision');
        if (!is_string($value) || !preg_match('/^[a-f0-9-]{36}$/D', $value)) throw new \RuntimeException('identity_revision');
        return $value;
    }

    // Called BEFORE original JWT signing. Unavailability must not break native
    // commerce login: omit the claim, so only the new support APIs fail closed.
    public static function stamp(int $id, string $realm, array $params, ?callable $read = null): array
    {
        unset($params[self::CLAIM]);
        if (!in_array($realm, ['api', 'kefu'], true)) return $params;
        try {
            $revision = $read ? $read($realm, $id) : self::current($realm, $id);
            if (!is_string($revision) || !preg_match('/^[a-f0-9-]{36}$/D', $revision)) throw new \RuntimeException('identity_revision');
            $params[self::CLAIM] = $revision;
        } catch (\Throwable $e) {
            // No raw error, token, or DB configuration in logs or response.
        }
        return $params;
    }

    public static function checkClaims(array $claims, string $realm, int $id, string $current): void
    {
        $revision = $claims[self::CLAIM] ?? null;
        $subject = $claims['jti'] ?? null;
        if (!is_array($subject) || ($subject['type'] ?? null) !== $realm ||
            (string)($subject['id'] ?? '') !== (string)$id ||
            !is_string($revision) || !preg_match('/^[a-f0-9-]{36}$/D', $current) ||
            !hash_equals($current, $revision)) throw new \RuntimeException('identity_revoked');
    }

    // Precondition: native parser has already verified signature, expiry,
    // login cache and realm. Decoding here alone is NOT authentication.
    public static function assertVerifiedToken(string $token, string $realm, int $id): void
    {
        $parts = explode('.', $token);
        if (count($parts) !== 3) throw new \RuntimeException('identity_revoked');
        $claims = json_decode(\Firebase\JWT\JWT::urlsafeB64Decode($parts[1]), true);
        if (!is_array($claims)) throw new \RuntimeException('identity_revoked');
        self::checkClaims($claims, $realm, $id, self::current($realm, $id));
    }
}
