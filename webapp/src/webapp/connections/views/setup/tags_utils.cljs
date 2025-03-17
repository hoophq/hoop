(ns webapp.connections.views.setup.tags-utils
  (:require
   [clojure.string :as str]))

(defn verify-tag-key
  "Verify if a tag key is not custom"
  [key]
  (when-let [matches (re-matches #"hoop\.dev/([^.]+)\..*" key)]
    (second matches)))

(defn extract-category
  "Extract category from a tag key like 'hoop.dev/infrastructure.environment' -> 'infrastructure'"
  [key]
  (when-let [matches (re-matches #"hoop\.dev/([^.]+)\..*" key)]
    (second matches)))

(defn extract-label
  "Extract label from a tag key like 'hoop.dev/infrastructure.environment' -> 'environment'"
  [key]
  (let [matches (re-matches #"hoop\.dev/[^.]+\.([^.]+)" key)]
    (if (second matches)
      (second matches)
      key)))

(defn format-label
  "Format a string like 'backup-policy' to 'Backup Policy'"
  [s]
  (when s
    (str/join " " (map str/capitalize (str/split s #"-")))))

(defn get-unique-keys-by-category
  "Get all unique keys organized by category
   Returns a structure like:
   [{:label 'Infrastructure',
     :options [{:value 'hoop.dev/infrastructure.cloud', :label 'cloud'},
               {:value 'hoop.dev/infrastructure.environment', :label 'environment'}, ...]}]"
  [tags-data]
  (let [items (:items tags-data)
        ;; Group by main category (Infrastructure, Security, etc.)
        by-category (group-by (comp extract-category :key) items)]

    (for [[category items] by-category
          :when category]
      (let [;; Get unique full keys with their subcategories
            unique-keys (distinct (map (fn [item]
                                         {:key (:key item)
                                          :label (extract-label (:key item))})
                                       items))]
        {:label (format-label category)
         :options (for [{:keys [key label]} unique-keys
                        :when label]
                    {:value key
                     :label label})}))))

(defn get-values-for-key
  "Get all available values for a specific key
   Returns a structure like:
   [{:value 'prod', :label 'prod'}, {:value 'staging', :label 'staging'}, ...]"
  [tags-data key-name]
  (let [items (:items tags-data)
        ;; Find items matching the key (either full key or just label)
        matching-items (filter (fn [item]
                                 (or (= (:key item) key-name)
                                     (= (extract-label (:key item)) key-name)))
                               items)]

    ;; Format the values for dropdown
    (for [item matching-items]
      {:value (:value item)
       :label (:value item)
       :id (:id item)})))

(defn format-keys-for-select
  "Format all available keys for the first select dropdown
   Returns grouped keys with their full path as value and label as label"
  [tags-data]
  (let [categorized-keys (get-unique-keys-by-category tags-data)]
    {:grouped-options categorized-keys
     :flat-options (mapcat :options categorized-keys)}))

;; Example usage:
;; 1. Get all keys formatted for first select:
;;    (format-keys-for-select mock-tags-data)
;;
;; 2. When user selects a key (e.g., "environment"):
;;    (get-values-for-key mock-tags-data "environment")
;;    => Returns all environment values (prod, staging, dev, etc.)
