(ns webapp.connections.views.resource-catalog.filters
  (:require
   ["@radix-ui/themes" :refer [Box Text Flex Badge]]
   ["lucide-react" :refer [Check]]
   [clojure.string :as cs]
   [webapp.components.forms :as forms]))

(defn search-section [search-term on-search-change]
  [:> Box {:class "space-y-radix-4"}
   [:> Text {:size "2" :weight "bold" :class "block text-[--gray-12]"}
    "Search"]
   [:> Box {:class "relative"}
    [forms/input {:placeholder "Resources or keywords"
                  :value search-term
                  :on-change #(on-search-change (.. % -target -value))}]]])

(defn categories-filter [selected-categories on-category-change all-categories]
  [:> Box {:class "space-y-radix-4"}
   [:> Text {:size "2" :weight "bold" :class "block text-[--gray-12]"}
    "Categories"]

   (for [category all-categories]
     ^{:key category}
     [:> Box {:class "flex items-center cursor-pointer space-x-3"
              :on-click #(on-category-change category)}
      [:> Text {:size "2" :class "text-[--gray-12] capitalize"}
       (cs/replace category #"-" " ")]
      (when (contains? selected-categories category)
        [:> Check {:size 16}])])])

(defn tags-filter [selected-tags on-tag-change all-tags]
  [:div {:class "space-y-radix-4"}
   [:> Text {:size "2" :weight "bold" :class "block text-[--gray-12]"}
    "Tags"]
   [:> Flex {:direction "row" :wrap "wrap" :gap "2"}
    (for [tag (take 15 all-tags)]
      ^{:key tag}
      [:> Badge {:variant (if (contains? selected-tags tag) "solid" "outline")
                 :color (if (contains? selected-tags tag) "" "gray")
                 :highContrast (if (contains? selected-tags tag) false true)
                 :size "2"
                 :class "cursor-pointer hover:opacity-80 transition-opacity"
                 :on-click #(on-tag-change tag)}
       tag])]])
