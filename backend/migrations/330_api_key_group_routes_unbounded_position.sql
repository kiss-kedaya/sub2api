-- 密钥级智能路由的 position 只表示「尝试顺序」，不是可绑定分组数量的上限。
-- 239 迁移里的 CHECK (position >= 1 AND position <= 10) 会让第 11 个分组
-- （position = 11）插入失败：分组超过 10 个的用户创建/编辑智能路由密钥时
-- 直接收到 api_key_group_routes_position_check 约束错误（issue #494）。
--
-- 这里只保留「position >= 1」的正序约束；UNIQUE (api_key_id, position)
-- 仍然保证同一把密钥内位置不重复。已存在的 1..10 数据不受影响，无需回填。
--
-- 幂等：先 DROP IF EXISTS；ADD 放在 DO 块里并按 pg_constraint 判断，
-- 重复执行不会因为约束已存在而报错。线上已有手工放宽过约束的库
-- （名字相同、定义已是 position >= 1）也会被规范成同一份定义。
ALTER TABLE api_key_group_routes
    DROP CONSTRAINT IF EXISTS api_key_group_routes_position_check;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'api_key_group_routes'::regclass
          AND conname = 'api_key_group_routes_position_check'
    ) THEN
        ALTER TABLE api_key_group_routes
            ADD CONSTRAINT api_key_group_routes_position_check
            CHECK (position >= 1);
    END IF;
END $$;
