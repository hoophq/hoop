(ns webapp.webclient.runbooks.list
  (:require
   ["@heroicons/react/20/solid" :as hero-solid-icon]
   ["@radix-ui/themes" :refer [Box Button]]
   ["lucide-react" :refer [File FolderClosed FolderOpen]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.tooltip :as tooltip]
   [webapp.config :as config]))

(defn sort-tree [data]
  (let [folders (->> data
                     (filter (fn [[k v]] (map? v)))
                     (sort-by first))
        files-or-list (->> data
                           (filter (fn [[k v]] (not (map? v))))
                           (sort-by first))]
    (into {} (concat folders files-or-list))))

(defn split-path [path]
  (cs/split path #"/"))

(defn is-file? [item]
  (or (string? item)
      (and (vector? item)
           (every? string? item))))

(defn insert-into-tree [tree path original-name]
  (if (empty? (rest path))
    (update tree (first path) (fnil conj []) original-name)
    (let [dir (first path)
          rest-path (rest path)
          sub-tree (get tree dir {})]
      (assoc tree dir (insert-into-tree sub-tree rest-path original-name)))))

(defn transform-payload [payload]
  (let [grouped-by-type (group-by
                         (fn [{:keys [name]}]
                           (if (cs/includes? name "/")
                             :with-folder
                             :without-folder))
                         payload)

        folder-tree (reduce
                     (fn [tree {:keys [name]}]
                       (let [path-parts (split-path name)]
                         (insert-into-tree tree path-parts name)))
                     {}
                     (:with-folder grouped-by-type))

        root-files (map (fn [{:keys [name]}]
                          [name name])
                        (:without-folder grouped-by-type))]

    (into folder-tree root-files)))

(defn file [filename filter-template-selected level]
  [:div {:class (str "flex items-center gap-2 pb-4 hover:underline cursor-pointer text-xs text-gray-12 whitespace-pre "
                     (when (pos? level)
                       "pl-4"))
         :on-click (fn []
                     (let [template (filter-template-selected filename)]
                       (rf/dispatch [:runbooks-plugin->set-active-runbook template])))}
   [:div
    [:> File {:size 14
              :class "text-[--gray-11]"}]]
   [:span {:class "block truncate"}
    [tooltip/truncate-tooltip {:text (last (split-path filename))}]]])

(defn directory []
  (let [dropdown-status (r/atom {})
        search-term (rf/subscribe [:search/term])]

    (fn [name items level filter-template-selected parent-path]
      (let [current-path (if parent-path (str parent-path "/" name) name)]
        (when (and (not (empty? @search-term))
                   (not= (get @dropdown-status name) :open))
          (swap! dropdown-status assoc name :open))

        (if (is-file? items)
          [file current-path filter-template-selected level]

          [:div {:class (str "text-xs text-gray-12 "
                             (when (pos? level)
                               "pl-4"))}
           [:div {:class "flex pb-4 items-center gap-small"}
            (if (= (get @dropdown-status name) :open)
              [:div
               [:> FolderOpen {:size 14
                               :class "text-[--gray-11]"}]]
              [:div
               [:> FolderClosed {:size 14
                                 :class "text-[--gray-11]"}]])
            [:span {:class (str "hover:underline cursor-pointer "
                                "flex items-center")
                    :on-click #(swap! dropdown-status
                                      assoc-in [name]
                                      (if (= (get @dropdown-status name) :open) :closed :open))}
             [:span name]
             (if (= (get @dropdown-status name) :open)
               [:> hero-solid-icon/ChevronUpIcon {:class "h-4 w-4 shrink-0 text-white"
                                                  :aria-hidden "true"}]
               [:> hero-solid-icon/ChevronDownIcon {:class "h-4 w-4 shrink-0 text-white"
                                                    :aria-hidden "true"}])]]

           [:div {:class (when (not= (get @dropdown-status name) :open)
                           "h-0 overflow-hidden")}
            (if (map? items)
              (let [subfolders-and-files (group-by (fn [[_ subcontents]]
                                                     (if (or (map? subcontents)
                                                             (and (vector? subcontents)
                                                                  (some map? subcontents)))
                                                       :folders
                                                       :files))
                                                   items)

                    sorted-subfolders (->> (get subfolders-and-files :folders {})
                                           (sort-by first))
                    sorted-files (->> (get subfolders-and-files :files {})
                                      (sort-by first))
                    child-level (inc level)]
                [:<>
                 (for [[subdirname subcontents] sorted-subfolders]
                   ^{:key subdirname}
                   [directory subdirname subcontents child-level filter-template-selected current-path])

                 (for [[filename subcontents] sorted-files]
                   ^{:key filename}
                   [directory filename subcontents child-level filter-template-selected current-path])])

              (for [item items]
                ^{:key item}
                [file item filter-template-selected (inc level)]))]])))))

(defn directory-tree [tree filter-template-selected]
  (let [folders-and-files (group-by (fn [[_ contents]]
                                      (if (or (map? contents)
                                              (and (vector? contents)
                                                   (some map? contents)))
                                        :folders
                                        :files))
                                    tree)
        sorted-folders (->> (get folders-and-files :folders [])
                            (sort-by first))
        sorted-files (->> (get folders-and-files :files [])
                          (sort-by first))]

    [:> Box
     (for [[dirname contents] sorted-folders]
       ^{:key dirname}
       [directory dirname contents 0 filter-template-selected nil])

     (for [[filename contents] sorted-files]
       ^{:key filename}
       [directory filename contents 0 filter-template-selected nil])]))

(defn- loading-list-view []
  [:div {:class "flex gap-small items-center py-regular text-xs text-gray-12"}
   [:span {:class "italic"}
    "Loading runbooks"]
   [:figure {:class "w-3 flex-shrink-0 animate-spin opacity-60"}
    [:img {:src (str config/webapp-url "/icons/icon-loader-circle-white.svg")}]]])

(defn- empty-templates-view []
  [:div {:class "text-center"}
   [:div {:class "text-gray-12 text-xs"}
    "There are no Runbooks available for this connection."]])

(defn- no-integration-templates-view []
  [:div {:class "pt-large"}
   [:div {:class "flex flex-col items-center text-center"}
    [:div {:class "text-gray-12 text-xs mb-large"}
     "Configure your Git repository to enable your Runbooks."]
    [:> Button {:color "indigo"
                :size "3"
                :variant "ghost"
                :radius "medium"
                :on-click #(rf/dispatch [:navigate :runbooks {:tab "configurations"}])}
     "Go to Configurations"]]])

(defn main []
  (fn [templates filtered-templates]
    (let [filter-template-selected (fn [template-name]
                                     (first (filter #(= (:name %) template-name) (:data @templates))))
          search-term (rf/subscribe [:search/term])
          transformed-payload (sort-tree (transform-payload @filtered-templates))]

      (cond
        (= :loading (:status @templates)) [loading-list-view]
        (= :error (:status @templates)) [no-integration-templates-view]
        (and (empty? (:data @templates)) (= :ready (:status @templates))) [empty-templates-view]
        (empty? @filtered-templates) [:div {:class "text-center text-xs text-gray-12 font-normal"}
                                      (if (empty? @search-term)
                                        "There are no runbooks available."
                                        (str "No runbooks matching \"" @search-term "\"."))]
        :else [directory-tree transformed-payload filter-template-selected]))))
