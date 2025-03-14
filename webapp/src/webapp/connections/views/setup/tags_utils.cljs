(ns webapp.connections.views.setup.tags-utils
  (:require
   [clojure.string :as str]))

;; Mock data for development purposes
(def mock-tags-data
  {:items
   [{:id "92da8216-dfb6-42c6-b96f-c4ed5be8d51a"
     :key "hoop.dev/infrastructure.environment"
     :value "prod"
     :updated_at "2025-03-14T17:29:07.518422Z"
     :created_at "2025-03-14T17:29:07.518422Z"}
    {:id "1e55e4c0-92b6-4bd5-9f6e-42deb124d231"
     :key "hoop.dev/infrastructure.environment"
     :value "staging"
     :updated_at "2025-03-14T17:29:07.518424Z"
     :created_at "2025-03-14T17:29:07.518424Z"}
    {:id "1b2aa4ef-ad07-4dfe-a336-c5dfa09695ff"
     :key "hoop.dev/infrastructure.environment"
     :value "dev"
     :updated_at "2025-03-14T17:29:07.518427Z"
     :created_at "2025-03-14T17:29:07.518427Z"}
    {:id "1f6aca9e-1c72-4b75-8e9e-83c7d31955c9"
     :key "hoop.dev/infrastructure.environment"
     :value "qa"
     :updated_at "2025-03-14T17:29:07.518429Z"
     :created_at "2025-03-14T17:29:07.518429Z"}
    {:id "91e3997a-cd00-45e5-8cdc-1d29f31683f2"
     :key "hoop.dev/infrastructure.environment"
     :value "sandbox"
     :updated_at "2025-03-14T17:29:07.51843Z"
     :created_at "2025-03-14T17:29:07.51843Z"}
    {:id "3c0c91d1-25a8-4cf9-91d3-ce462276c719"
     :key "hoop.dev/infrastructure.backup-policy"
     :value "daily"
     :updated_at "2025-03-14T17:29:07.518432Z"
     :created_at "2025-03-14T17:29:07.518432Z"}
    {:id "8b6c715e-cdc3-44f4-af18-ceb1db289a31"
     :key "hoop.dev/infrastructure.backup-policy"
     :value "weekly"
     :updated_at "2025-03-14T17:29:07.518433Z"
     :created_at "2025-03-14T17:29:07.518433Z"}
    {:id "9a884caf-fca8-4335-a3a5-d85ed32d8d39"
     :key "hoop.dev/infrastructure.backup-policy"
     :value "monthly"
     :updated_at "2025-03-14T17:29:07.518434Z"
     :created_at "2025-03-14T17:29:07.518434Z"}
    {:id "01396684-8daf-4cfc-923f-3a877619a515"
     :key "hoop.dev/infrastructure.backup-policy"
     :value "none"
     :updated_at "2025-03-14T17:29:07.518435Z"
     :created_at "2025-03-14T17:29:07.518435Z"}
    {:id "318c8685-0926-4006-9088-3920410e4813"
     :key "hoop.dev/security.security-zone"
     :value "public"
     :updated_at "2025-03-14T17:29:07.518436Z"
     :created_at "2025-03-14T17:29:07.518436Z"}
    {:id "dfe1350c-b3f2-49a3-80dd-afd53076f233"
     :key "hoop.dev/security.security-zone"
     :value "private"
     :updated_at "2025-03-14T17:29:07.518438Z"
     :created_at "2025-03-14T17:29:07.518438Z"}]})

(defn extract-category
  "Extract category from a tag key like 'hoop.dev/infrastructure.environment' -> 'infrastructure'"
  [key]
  (when-let [matches (re-matches #"hoop\.dev/([^.]+)\..*" key)]
    (second matches)))

(defn extract-subcategory
  "Extract subcategory from a tag key like 'hoop.dev/infrastructure.environment' -> 'environment'"
  [key]
  (when-let [matches (re-matches #"hoop\.dev/[^.]+\.([^.]+)" key)]
    (second matches)))

(defn format-label
  "Format a string like 'backup-policy' to 'Backup Policy'"
  [s]
  (when s
    (str/join " " (map str/capitalize (str/split s #"-")))))

(defn parse-tags-for-group-select
  "Parse tags data into a grouped structure suitable for dropdown selection"
  [tags-data]
  (let [items (:items tags-data)
        ;; Group tags by category (infrastructure, security, etc.)
        grouped-by-category (group-by (comp extract-category :key) items)

        ;; Transform each category into the expected format
        categories (for [[category items] grouped-by-category
                         :when category]
                     {:label (format-label category)
                      :options
                      ;; Group items within category by subcategory
                      (let [by-subcategory (group-by (comp extract-subcategory :key) items)]
                        (for [[subcategory subitems] by-subcategory
                              :when subcategory]
                          {:label (format-label subcategory)
                           :options
                           ;; Create options for each value
                           (for [item subitems]
                             {:value (:value item)
                              :label (:value item)
                              :key (:key item)
                              :id (:id item)})}))})

        ;; Sort categories by label
        sorted-categories (sort-by :label categories)]

    sorted-categories))

(defn tag-key-to-display-name
  "Convert a tag key to a display-friendly name
   e.g., 'hoop.dev/infrastructure.environment' -> 'environment'"
  [key]
  (extract-subcategory key))

(defn format-for-dropdown
  "Format tags data specifically for a dropdown component
   Returns a flatter structure with categories and options

   Example output:
   [{:label 'Infrastructure',
     :options [{:value 'hoop.dev/infrastructure.environment', :label 'environment'}, ...]}]
   "
  [tags-data]
  (let [items (:items tags-data)
        ;; Group by the main category (part after hoop.dev/)
        grouped-by-category (group-by (comp extract-category :key) items)

        categories
        (for [[category items] grouped-by-category
              :when category]
          {:label (format-label category)
           :options
           ;; Group by subcategory (like environment, cloud, etc.)
           (let [by-subcategory (group-by (comp extract-subcategory :key) items)]
             (for [[subcategory subitems] by-subcategory
                   :when subcategory
                   :let [full-key (str "hoop.dev/" category "." subcategory)]]
               ;; Create a single option for each subcategory with full path as value
               {:value full-key
                :label subcategory
                :subcategory subcategory
                :category category}))})]

    (sort-by :label categories)))

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
                                          :subcategory (extract-subcategory (:key item))})
                                       items))]
        {:label (format-label category)
         :options (for [{:keys [key subcategory]} unique-keys
                        :when subcategory]
                    {:value key
                     :label subcategory})}))))

(defn get-values-for-key
  "Get all available values for a specific key
   Returns a structure like:
   [{:value 'prod', :label 'prod'}, {:value 'staging', :label 'staging'}, ...]"
  [tags-data key-name]
  (let [items (:items tags-data)
        ;; Find items matching the key (either full key or just subcategory)
        matching-items (filter (fn [item]
                                 (or (= (:key item) key-name)
                                     (= (extract-subcategory (:key item)) key-name)))
                               items)]

    ;; Format the values for dropdown
    (for [item matching-items]
      {:value (:value item)
       :label (:value item)
       :id (:id item)})))

(defn key-for-ui
  "Transform a full key like 'hoop.dev/infrastructure.environment'
   to a simpler representation for UI, like 'environment'"
  [full-key]
  (extract-subcategory full-key))

(defn format-keys-for-select
  "Format all available keys for the first select dropdown
   Returns grouped keys with their full path as value and subcategory as label"
  [tags-data]
  (let [categorized-keys (get-unique-keys-by-category tags-data)]
    {:grouped-options categorized-keys
     :flat-options (mapcat :options categorized-keys)}))

(defn get-full-key-from-subcategory
  "Get the full key from a subcategory name.
   e.g., 'environment' -> 'hoop.dev/infrastructure.environment'"
  [tags-data subcategory]
  (let [items (:items tags-data)
        matching-item (first (filter #(= (extract-subcategory (:key %)) subcategory) items))]
    (when matching-item
      (:key matching-item))))

;; Function to prepare tags for backend submission
(defn prepare-tags-for-backend
  "Transform tags from internal format to backend format
   Input: [{:key 'hoop.dev/infrastructure.environment', :value 'prod'}, ...]
   Output: {'environment': 'prod', ...}"
  [tags]
  (reduce (fn [acc {:keys [key subcategory value]}]
            (let [subcategory (or subcategory (extract-subcategory key))]
              (assoc acc subcategory value)))
          {}
          tags))

;; Example usage:
;; 1. Get all keys formatted for first select:
;;    (format-keys-for-select mock-tags-data)
;;
;; 2. When user selects a key (e.g., "environment"):
;;    (get-values-for-key mock-tags-data "environment")
;;    => Returns all environment values (prod, staging, dev, etc.)
